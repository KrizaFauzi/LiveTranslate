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
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	owner   = "11111111-2222-4333-8444-555555555555"
	other   = "66666666-2222-4333-8444-555555555555"
	meeting = "aaaaaaaa-2222-4333-8444-555555555555"
	runID   = "bbbbbbbb-2222-4333-8444-555555555555"
	// orphanRun is a run of an earlier backend instance: the row is active but
	// no transcription session is left for it.
	orphanRun = "dddddddd-2222-4333-8444-555555555555"
)

func jsonResponse(status int, value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}

// fakeStream is a provider stream that accepts every audio chunk and publishes
// one final segment when the session asks for the pending transcript.
type fakeStream struct {
	mu      sync.Mutex
	writes  [][]byte
	closed  bool
	results chan transcribe.Result
	errors  chan error
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

// workingProvider opens a stream that accepts every audio chunk.
type workingProvider struct {
	stream *fakeStream
}

func (p workingProvider) Open(context.Context, int) (transcribe.Stream, error) {
	if p.stream == nil {
		return newFakeStream(), nil
	}
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
func newHandler(t *testing.T, provider transcribe.Provider) (http.Handler, *[]string) {
	t.Helper()
	var mu sync.Mutex
	bytesSeen := int64(0)
	chunksSeen := 0
	runStatus := "active"
	paths := []string{}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
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
			if r.URL.Query().Get("id") == "eq."+meeting {
				return jsonResponse(200, []map[string]string{{"id": meeting}}), nil
			}
			return jsonResponse(200, []map[string]string{}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "POST":
			runStatus = "active"
			return jsonResponse(201, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "status": "active",
				"bytes_received": 0, "chunks_received": 0, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil,
			}}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "PATCH":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			runStatus = body["status"]
			return jsonResponse(200, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "status": runStatus,
				"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil,
			}}), nil
		case r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "GET":
			if r.Header.Get("Authorization") != "Bearer owner" {
				return jsonResponse(200, []map[string]any{}), nil
			}
			if r.URL.Query().Get("id") == "eq."+orphanRun {
				return jsonResponse(200, []map[string]any{{
					"id": orphanRun, "user_id": owner, "meeting_id": meeting, "status": "active",
					"bytes_received": 0, "chunks_received": 0, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil,
				}}), nil
			}
			return jsonResponse(200, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "status": runStatus,
				"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil,
			}}), nil
		case r.URL.Path == "/rest/v1/rpc/append_audio_chunk":
			var body struct {
				RunUUID    string `json:"run_uuid"`
				ChunkBytes int    `json:"chunk_bytes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if runStatus != "active" {
				return jsonResponse(200, []map[string]any{}), nil
			}
			bytesSeen += int64(body.ChunkBytes)
			chunksSeen++
			return jsonResponse(200, []map[string]any{{
				"id": runID, "user_id": owner, "meeting_id": meeting, "status": "active",
				"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": nil,
			}}), nil
		case r.URL.Path == "/rest/v1/rpc/stop_audio_run":
			if runStatus == "active" {
				runStatus = "stopped"
				return jsonResponse(200, []map[string]any{{
					"id": runID, "user_id": owner, "meeting_id": meeting, "status": "stopped",
					"bytes_received": bytesSeen, "chunks_received": chunksSeen, "started_at": "2026-10-21T00:00:00Z", "ended_at": "2026-10-21T00:01:00Z",
				}}), nil
			}
			return jsonResponse(200, []map[string]any{}), nil
		}
		return jsonResponse(404, map[string]string{"message": "unknown"}), nil
	})}
	broker := transcribe.NewBroker(config.Config{}, provider)
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client, broker)
	return h, &paths
}

func call(t *testing.T, h http.Handler, method, path, body, token, contentType string) *httptest.ResponseRecorder {
	t.Helper()
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
	h, paths := newHandler(t, failingProvider{})
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
	for _, path := range *paths {
		if path == "PATCH /rest/v1/meeting_runs" {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("the failed run was not marked failed: %v", *paths)
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
	h, paths := newHandler(t, unconfiguredProvider{})
	w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json")
	if w.Code != 503 {
		t.Fatalf("unconfigured provider: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "deepgram_config") {
		t.Fatalf("unexpected error code: %s", w.Body.String())
	}
	for _, path := range *paths {
		if path == "POST /rest/v1/meeting_runs" {
			t.Fatalf("the run row was created: %v", *paths)
		}
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
