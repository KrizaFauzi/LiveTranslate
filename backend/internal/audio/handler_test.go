package audio

import (
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	owner        = "11111111-2222-4333-8444-555555555555"
	other        = "66666666-2222-4333-8444-555555555555"
	meeting      = "aaaaaaaa-2222-4333-8444-555555555555"
	runID        = "bbbbbbbb-2222-4333-8444-555555555555"
	occurrenceID = "abababab-2222-4333-8444-555555555555"
	// orphanRun is a run of an earlier backend instance: the row is active but
	// no transcription session is left for it.
	orphanRun = "dddddddd-2222-4333-8444-555555555555"
	// stoppedRun is a run that is already over: the row is stopped and no
	// transcription session is left for it.
	stoppedRun = "eeeeeeee-2222-4333-8444-555555555555"
	// unknownRun is a run that does not exist.
	unknownRun = "ffffffff-2222-4333-8444-555555555555"
)

func jsonResponse(status int, value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

// fakeDB is the state of the in-memory Data API. The tests use it to observe
// the calls of the handler and to arm a slow or failing bookkeeping.
type fakeDB struct {
	mu             sync.Mutex
	paths          []string
	patchStatus    string
	stopFails      bool
	stopDelay      time.Duration
	ownershipDelay time.Duration
}

// slowOwnership delays the ownership reads of the Data API.
func (db *fakeDB) slowOwnership(delay time.Duration) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.ownershipDelay = delay
}

// slowPause waits for the delay, or until the request ends. It reports the
// error of the request when the deadline of the caller expires first.
func slowPause(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// lastPatchStatus returns the status of the last PATCH of the Data API.
func (db *fakeDB) lastPatchStatus() string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.patchStatus
}

// calls returns the recorded calls of the Data API.
func (db *fakeDB) calls() []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]string(nil), db.paths...)
}

// failStop makes the stop bookkeeping fail.
func (db *fakeDB) failStop() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.stopFails = true
}

// slowStop delays the stop bookkeeping.
func (db *fakeDB) slowStop(delay time.Duration) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.stopDelay = delay
}

// fakeStream is a provider stream that accepts every audio chunk and publishes
// one final segment when the session asks for the pending transcript.
type fakeStream struct {
	mu            sync.Mutex
	writes        [][]byte
	closed        bool
	ctx           context.Context
	finalizeDelay time.Duration
	results       chan transcribe.Result
	errors        chan error
}

func newFakeStream() *fakeStream {
	return &fakeStream{results: make(chan transcribe.Result, 4), errors: make(chan error, 1)}
}

func (s *fakeStream) Write(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]byte(nil), payload...))
	return nil
}

// Finalize publishes the pending transcript so the session flush does not wait.
func (s *fakeStream) Finalize() error {
	if delay := s.finalizeDelay; delay > 0 {
		time.Sleep(delay)
	}
	select {
	case s.results <- transcribe.Result{Text: "selamat pagi", IsFinal: true}:
	default:
	}
	return nil
}

func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeStream) Results() <-chan transcribe.Result { return s.results }

func (s *fakeStream) Errors() <-chan error { return s.errors }

// writeCount reports how many payloads reached the provider.
func (s *fakeStream) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

// isClosed reports whether the provider stream was closed.
func (s *fakeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// setContext keeps the context that the provider received, so the test can
// check that the context of the session outlives the request.
func (s *fakeStream) setContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
}

// context returns the context that the provider received.
func (s *fakeStream) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// workingProvider opens a stream that accepts every audio chunk.
type workingProvider struct {
	stream *fakeStream
}

func (p workingProvider) Open(ctx context.Context, _ int) (transcribe.Stream, error) {
	if p.stream == nil {
		return newFakeStream(), nil
	}
	p.stream.setContext(ctx)
	return p.stream, nil
}

// failingProvider refuses every connection, like a provider that cannot be reached.
type failingProvider struct{}

func (failingProvider) Open(context.Context, int) (transcribe.Stream, error) {
	return nil, transcribe.ErrProviderConnect
}

// unconfiguredProvider reports that the provider configuration is incomplete.
type unconfiguredProvider struct{}

func (unconfiguredProvider) Open(context.Context, int) (transcribe.Stream, error) {
	return nil, transcribe.ErrProviderConfig
}

// Configured reports that the provider cannot open a stream yet.
func (unconfiguredProvider) Configured() bool { return false }

// newHandler wires the handler to an in-memory Data API that mimics the RLS and function behaviour.
// The provider decides whether the transcription session of a run can be opened.
func newHandler(t *testing.T, provider transcribe.Provider) (http.Handler, *fakeDB) {
	t.Helper()
	h, _, db := newHandlerWithBroker(t, provider)
	return h, db
}

// newHandlerWithBroker also returns the broker, so a test can open the live
// session of a run.
func newHandlerWithBroker(t *testing.T, provider transcribe.Provider) (http.Handler, *transcribe.Broker, *fakeDB) {
	t.Helper()
	db := &fakeDB{}
	bytesSeen := int64(0)
	chunksSeen := 0
	runStatus := "active"
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.paths = append(db.paths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/auth/v1/user":
			switch r.Header.Get("Authorization") {
			case "Bearer owner":
				return jsonResponse(200, map[string]string{"id": owner}), nil
			case "Bearer other":
				return jsonResponse(200, map[string]string{"id": other}), nil
			}
			return jsonResponse(401, map[string]string{"msg": "invalid"}), nil
		case r.URL.Path == "/rest/v1/meeting_sessions":
			if err := slowPause(r.Context(), db.ownershipDelay); err != nil {
				return nil, err
			}
			if r.URL.Query().Get("id") == "eq."+meeting {
				return jsonResponse(200, []map[string]string{{"id": meeting}}), nil
			}
			return jsonResponse(200, []map[string]string{}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "POST":
			runStatus = "active"
			return jsonResponse(201, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": "active",
				"bytes_received": 0, "chunks_received": 0, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil, "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
			}}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "PATCH":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			db.patchStatus = body["status"]
			runStatus = body["status"]
			return jsonResponse(200, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": runStatus,
				"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil, "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
			}}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "GET":
			if err := slowPause(r.Context(), db.ownershipDelay); err != nil {
				return nil, err
			}
			if r.Header.Get("Authorization") != "Bearer owner" {
				return jsonResponse(200, []map[string]any{}), nil
			}
			switch r.URL.Query().Get("id") {
			case "eq." + orphanRun:
				return jsonResponse(200, []map[string]any{{
					"id": orphanRun, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": "active",
					"bytes_received": 0, "chunks_received": 0, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil, "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
				}}), nil
			case "eq." + stoppedRun:
				return jsonResponse(200, []map[string]any{{
					"id": stoppedRun, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": "stopped",
					"bytes_received": 0, "chunks_received": 0, "started_at": "2026-10-21T00:00:00Z", "ended_at": "2026-10-21T00:01:00Z", "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
				}}), nil
			case "eq." + runID:
				return jsonResponse(200, []map[string]any{{
					"id": runID, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": runStatus,
					"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil, "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
				}}), nil
			}
			return jsonResponse(200, []map[string]any{}), nil
		case r.URL.Path == "/rest/v1/rpc/append_audio_chunk":
			var body struct {
				RunUUID    string `json:"run_uuid"`
				ChunkBytes int    `json:"chunk_bytes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Header.Get("Authorization") != "Bearer owner" || runStatus != "active" {
				return jsonResponse(200, []map[string]any{}), nil
			}
			bytesSeen += int64(body.ChunkBytes)
			chunksSeen++
			return jsonResponse(200, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": "active",
				"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil, "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
			}}), nil
		case r.URL.Path == "/rest/v1/rpc/stop_audio_run":
			if r.Header.Get("Authorization") != "Bearer owner" {
				return jsonResponse(200, []map[string]any{}), nil
			}
			if db.stopDelay > 0 {
				if err := slowPause(r.Context(), db.stopDelay); err != nil {
					return nil, err
				}
			}
			if db.stopFails {
				return jsonResponse(500, map[string]string{"message": "bookkeeping gagal"}), nil
			}
			if runStatus == "active" {
				runStatus = "stopped"
				return jsonResponse(200, []map[string]any{{
					"id": runID, "user_id": owner, "meeting_id": meeting, "occurrence_id": occurrenceID, "status": "stopped",
					"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": "2026-10-21T00:01:00Z", "save_transcript": true, "transcript_state": "saving", "transcript_expires_at": "2099-01-01T00:00:00Z",
				}}), nil
			}
			return jsonResponse(200, []map[string]any{}), nil
		}
		return jsonResponse(404, map[string]string{"message": "unknown"}), nil
	})}
	broker := transcribe.NewBroker(config.Config{}, provider)
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client, broker)
	h.(*handler).now = func() time.Time { return time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC) }
	return h, broker, db
}

func TestRunRequestRequiresOccurrenceAndRejectsLegacyArchiveConsent(t *testing.T) {
	meetingID, gotOccurrence, rate, valid := parseRunRequest([]byte(`{"meeting_id":"` + meeting + `","occurrence_id":"` + occurrenceID + `","sample_rate":48000}`))
	if !valid || meetingID != meeting || gotOccurrence != occurrenceID || rate != 48000 {
		t.Fatalf("valid occurrence-bound request rejected: %q %q %d %v", meetingID, gotOccurrence, rate, valid)
	}
	for _, body := range []string{
		`{"meeting_id":"` + meeting + `","sample_rate":48000}`,
		`{"meeting_id":"` + meeting + `","occurrence_id":"` + occurrenceID + `","sample_rate":48000,"save_transcript":true}`,
	} {
		if _, _, _, accepted := parseRunRequest([]byte(body)); accepted {
			t.Fatalf("legacy or unbound request accepted: %s", body)
		}
	}
}

func call(t *testing.T, h http.Handler, method, path, body, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	if method == http.MethodPost && path == "/api/audio/runs" && contentType == "application/json" {
		var input map[string]any
		if json.Unmarshal([]byte(body), &input) == nil {
			input["occurrence_id"] = occurrenceID
			delete(input, "save_transcript")
			encoded, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			body = string(encoded)
		}
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRunLifecycle(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`"}`, "", "application/json"); w.Code != 401 {
		t.Fatalf("missing auth: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`"}`, "bogus", "application/json"); w.Code != 401 {
		t.Fatalf("invalid auth: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"not-a-uuid"}`, "owner", "application/json"); w.Code != 400 {
		t.Fatalf("invalid meeting id: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","user_id":"forged"}`, "owner", "application/json"); w.Code != 400 {
		t.Fatalf("forged field: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"cccccccc-2222-4333-8444-555555555555","sample_rate":48000}`, "owner", "application/json"); w.Code != 404 {
		t.Fatalf("unknown meeting: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`"}`, "owner", "text/plain"); w.Code != 415 {
		t.Fatalf("content type: %d %s", w.Code, w.Body.String())
	}
	w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	if w.Code != 201 || !strings.Contains(w.Body.String(), runID) {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control: %q", w.Header().Get("Cache-Control"))
	}
}

func TestRunRequiresSampleRateAndProvider(t *testing.T) {
	h, db := newHandler(t, failingProvider{})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`"}`, "owner", "application/json"); w.Code != 400 {
		t.Fatalf("missing sample rate: %d %s", w.Code, w.Body.String())
	}
	failed := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	if failed.Code != 502 {
		t.Fatalf("provider failure: %d %s", failed.Code, failed.Body.String())
	}
	if !strings.Contains(failed.Body.String(), "deepgram_unreachable") {
		t.Fatalf("unexpected error code: %s", failed.Body.String())
	}
	marked := false
	for _, path := range db.calls() {
		if path == "PATCH /rest/v1/meeting_runs" {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("the failed run was not marked failed: %v", db.calls())
	}
	if status := db.lastPatchStatus(); status != "failed" {
		t.Fatalf("unexpected patched status: %q", status)
	}
}

func TestRunRejectsInvalidSampleRate(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	for _, body := range []string{
		`{"meeting_id":"` + meeting + `","sample_rate":0}`,
		`{"meeting_id":"` + meeting + `","sample_rate":-1}`,
		`{"meeting_id":"` + meeting + `","sample_rate":"48000"}`,
		`{"meeting_id":"` + meeting + `","sample_rate":48000.5}`,
	} {
		if w := call(t, h, "POST", "/api/audio/runs", body, "owner", "application/json"); w.Code != 400 {
			t.Fatalf("invalid sample rate: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRunWithoutProviderConfigurationCreatesNoRow(t *testing.T) {
	h, db := newHandler(t, unconfiguredProvider{})
	w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	if w.Code != 503 {
		t.Fatalf("unconfigured provider: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "deepgram_config") {
		t.Fatalf("unexpected error code: %s", w.Body.String())
	}
	for _, path := range db.calls() {
		if path == "POST /rest/v1/meeting_runs" {
			t.Fatalf("the run row was created: %v", db.calls())
		}
	}
}

func TestSessionContextOutlivesTheRunRequest(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	sessionCtx := stream.context()
	if sessionCtx == nil {
		t.Fatal("the provider did not receive a context")
	}
	if err := sessionCtx.Err(); err != nil {
		t.Fatalf("the session context died together with the request: %v", err)
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio", "owner", "application/octet-stream"); w.Code != 200 {
		t.Fatalf("chunk: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json"); w.Code != 200 {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if err := sessionCtx.Err(); err == nil {
		t.Fatal("the session context outlived the session")
	}
}

func TestChunkAccountingAndLimits(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	first := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio-bytes", "owner", "application/octet-stream")
	if first.Code != 200 {
		t.Fatalf("chunk: %d %s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"bytes_received":11`) || !strings.Contains(first.Body.String(), `"chunks_received":1`) {
		t.Fatalf("accounting: %s", first.Body.String())
	}
	if stream.writeCount() != 1 {
		t.Fatalf("the chunk did not reach the provider: %d writes", stream.writeCount())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "", "owner", "application/octet-stream"); w.Code != 400 {
		t.Fatalf("empty chunk: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", strings.Repeat("x", maxChunkBytes+1), "owner", "application/octet-stream"); w.Code != 413 {
		t.Fatalf("oversized chunk: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio", "other", "application/octet-stream"); w.Code != 404 {
		t.Fatalf("foreign run: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio", "", "application/octet-stream"); w.Code != 401 {
		t.Fatalf("unauthenticated chunk: %d %s", w.Code, w.Body.String())
	}
}

func TestChunkStopsRunAtFourHourLimit(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	h.(*handler).now = func() time.Time { return time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC) }
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio", "owner", "application/octet-stream")
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"occurrence_limit"`) || stream.writeCount() != 0 {
		t.Fatalf("four-hour cap: %d %s writes=%d", w.Code, w.Body.String(), stream.writeCount())
	}
}

func TestStopIsIdempotentAndRejectsLateChunks(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json"); w.Code != 200 {
		t.Fatalf("second stop: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "late-audio", "owner", "application/octet-stream"); w.Code != 409 {
		t.Fatalf("chunk after stop: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "other", "application/json"); w.Code != 404 {
		t.Fatalf("foreign stop: %d %s", w.Code, w.Body.String())
	}
}

func TestStopOfAnotherAccountDoesNotCloseTheSession(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "other", "application/json")
	if w.Code != 404 {
		t.Fatalf("foreign stop: %d %s", w.Code, w.Body.String())
	}
	if stream.isClosed() {
		t.Fatal("the stop of another account closed the session")
	}
}

func TestStopRemovesTheSessionWhenTheBookkeepingFails(t *testing.T) {
	stream := newFakeStream()
	h, db := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	db.failStop()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	if w.Code != 502 {
		t.Fatalf("stop with a failing bookkeeping: %d %s", w.Code, w.Body.String())
	}
	if !stream.isClosed() {
		t.Fatal("the session was not closed")
	}
	// The closed session must leave the broker, so the next chunk is rejected as
	// a closed run instead of reaching a dead session.
	chunk := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio", "owner", "application/octet-stream")
	if chunk.Code != 409 {
		t.Fatalf("chunk after a failed stop: %d %s", chunk.Code, chunk.Body.String())
	}
}

func TestStopBookkeepingRunsWhileTheSessionCloses(t *testing.T) {
	stream := newFakeStream()
	stream.finalizeDelay = 2 * time.Second
	h, db := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	db.slowStop(2 * time.Second)
	start := time.Now()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	elapsed := time.Since(start)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	// The close and the bookkeeping run at the same time, so the route waits for
	// the slower one and stays below the write timeout of the server. One after
	// the other would take about four seconds.
	if elapsed >= 3*time.Second {
		t.Fatalf("the stop waited for the close and the bookkeeping one after the other: %s", elapsed)
	}
}

func TestStopOwnershipReadIsBounded(t *testing.T) {
	original := ownershipBudget
	ownershipBudget = 200 * time.Millisecond
	defer func() { ownershipBudget = original }()
	stream := newFakeStream()
	h, db := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	db.slowOwnership(2 * time.Second)
	start := time.Now()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	elapsed := time.Since(start)
	if w.Code != 504 {
		t.Fatalf("stop with a slow ownership read: %d %s", w.Code, w.Body.String())
	}
	if elapsed >= time.Second {
		t.Fatalf("the ownership read was not bounded: %s", elapsed)
	}
	if stream.isClosed() {
		t.Fatal("the session was closed although the ownership read failed")
	}
}

func TestRunCreationIsBounded(t *testing.T) {
	original := startBudget
	startBudget = 200 * time.Millisecond
	defer func() { startBudget = original }()
	h, db := newHandler(t, workingProvider{})
	db.slowOwnership(2 * time.Second)
	start := time.Now()
	w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	elapsed := time.Since(start)
	if w.Code != 504 {
		t.Fatalf("run creation with a slow ownership read: %d %s", w.Code, w.Body.String())
	}
	if elapsed >= time.Second {
		t.Fatalf("the run creation was not bounded: %s", elapsed)
	}
}

func TestRouteBudgetsStayBelowTheServerWriteTimeout(t *testing.T) {
	// The server writes the response within 2 * config.ConnectionTimeout, so the
	// phases of a route have to fit in that window.
	if ownershipBudget > 5*time.Second {
		t.Fatalf("the ownership read may take %s", ownershipBudget)
	}
	if closeBudget > 10*time.Second {
		t.Fatalf("the session close may take %s", closeBudget)
	}
	if bookkeepingBudget > 10*time.Second {
		t.Fatalf("the run bookkeeping may take %s", bookkeepingBudget)
	}
	if ownershipBudget+closeBudget >= 2*config.ConnectionTimeout {
		t.Fatalf("the stop route can outlive the write timeout of the server: %s", ownershipBudget+closeBudget)
	}
	if startBudget+10*time.Second+failureBudget >= 2*config.ConnectionTimeout {
		t.Fatalf("the run creation can outlive the write timeout of the server: %s", startBudget+10*time.Second+failureBudget)
	}
}

func TestChunkAfterStopKeepsByteAccounting(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio-bytes", "owner", "application/octet-stream"); w.Code != 200 {
		t.Fatalf("chunk: %d %s", w.Code, w.Body.String())
	}
	stopped := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	if stopped.Code != 200 || !strings.Contains(stopped.Body.String(), `"status":"stopped"`) {
		t.Fatalf("stop: %d %s", stopped.Code, stopped.Body.String())
	}
	if !stream.isClosed() {
		t.Fatal("the stop route did not close the transcription session")
	}
	rejected := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "late-audio", "owner", "application/octet-stream")
	if rejected.Code != 409 {
		t.Fatalf("chunk after stop: %d %s", rejected.Code, rejected.Body.String())
	}
	if !strings.Contains(rejected.Body.String(), "run_closed") {
		t.Fatalf("unexpected error code: %s", rejected.Body.String())
	}
	after := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "application/json")
	if after.Code != 200 || !strings.Contains(after.Body.String(), `"bytes_received":11`) {
		t.Fatalf("the byte accounting changed: %d %s", after.Code, after.Body.String())
	}
}

func TestChunkWithoutLiveSessionIsRejected(t *testing.T) {
	stream := newFakeStream()
	h, _ := newHandler(t, workingProvider{stream: stream})
	w := call(t, h, "POST", "/api/audio/runs/"+orphanRun+"/chunks", "audio-bytes", "owner", "application/octet-stream")
	if w.Code != 409 {
		t.Fatalf("chunk without a live session: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "run_closed") {
		t.Fatalf("unexpected error code: %s", w.Body.String())
	}
	if stream.writeCount() != 0 {
		t.Fatalf("the chunk reached the provider: %d writes", stream.writeCount())
	}
}

// TestProviderEndEvictsTheSessionAndFailsTheRun covers a provider that dies on
// its own: the session leaves the broker and the run is marked failed, so the
// chunk route answers with a closed run and the run does not stay active.
func TestProviderEndEvictsTheSessionAndFailsTheRun(t *testing.T) {
	stream := newFakeStream()
	h, broker, db := newHandlerWithBroker(t, workingProvider{stream: stream})
	started := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	if started.Code != 201 {
		t.Fatalf("start: %d %s", started.Code, started.Body.String())
	}
	if _, live := broker.Session(runID); !live {
		t.Fatal("the session is not in the broker")
	}
	// The provider ends its stream: the session cannot continue.
	close(stream.results)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, live := broker.Session(runID); !live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dead session stayed in the broker")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for db.lastPatchStatus() != "failed" {
		if time.Now().After(deadline) {
			t.Fatalf("the run was not marked failed: %q", db.lastPatchStatus())
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The chunk route answers with a closed run, not with a provider failure.
	rejected := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "audio-bytes", "owner", "application/octet-stream")
	if rejected.Code != 409 {
		t.Fatalf("chunk after the provider died: %d %s", rejected.Code, rejected.Body.String())
	}
	if !strings.Contains(rejected.Body.String(), "run_closed") {
		t.Fatalf("unexpected error code: %s", rejected.Body.String())
	}
}

func TestDecodeOccurrenceRunAcceptsPermanentNullExpiry(t *testing.T) {
	data := []byte(`[{"id":"aaaaaaaa-2222-4333-8444-555555555555","user_id":"11111111-2222-4333-8444-555555555555","meeting_id":"bbbbbbbb-2222-4333-8444-555555555555","occurrence_id":"cccccccc-2222-4333-8444-555555555555","status":"active","save_transcript":true,"transcript_state":"saving","transcript_expires_at":null}]`)
	rows, failure := decodeRuns(data, "11111111-2222-4333-8444-555555555555")
	if failure != nil || len(rows) != 1 || rows[0].TranscriptExpiresAt != "" {
		t.Fatalf("permanent occurrence run rejected: rows=%+v failure=%+v", rows, failure)
	}
}

func TestCreateRunMapsActiveOwnerConflict(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusConflict, map[string]string{"code": "23505"}), nil
	})}
	repo := newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client)
	_, failure := repo.create(context.Background(), meeting, occurrenceID, owner, "owner")
	if failure == nil || failure.status != http.StatusConflict || failure.code != "active_run_exists" {
		t.Fatalf("unexpected conflict mapping: %+v", failure)
	}
}
