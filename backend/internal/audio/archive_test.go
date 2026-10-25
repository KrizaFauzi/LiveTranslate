package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
)

// archiveDB models the durable Data API boundary: owner filtering, immutable
// sequence identities, terminal delete, and the exact snapshot filters.
type archiveDB struct {
	mu       sync.Mutex
	run      Run
	segments map[int]storedSegment
	appends  int
	ack      chan int
	tokens   []string
}

func newArchiveHandler(t *testing.T) (*handler, *archiveDB, *fakeStream) {
	t.Helper()
	db := &archiveDB{run: Run{ID: runID, UserID: owner, MeetingID: meeting, OccurrenceID: occurrenceID, Status: "active", StartedAt: "2026-10-23T00:00:00Z", SaveTranscript: true, TranscriptState: "saving", TranscriptExpiresAt: "2099-01-01T00:00:00Z"}, segments: map[int]storedSegment{}, ack: make(chan int, 512)}
	stream := newFakeStream()
	client := &http.Client{Transport: transportFunc(db.roundTrip)}
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client, transcribe.NewBroker(config.Config{}, workingProvider{stream: stream})).(*handler)
	h.now = func() time.Time { return time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC) }
	return h, db, stream
}

func (db *archiveDB) roundTrip(r *http.Request) (*http.Response, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	token := r.Header.Get("Authorization")
	if r.URL.Path == "/auth/v1/user" {
		if token == "Bearer owner" || token == "Bearer fresh" {
			return jsonResponse(200, map[string]string{"id": owner}), nil
		}
		if token == "Bearer other" {
			return jsonResponse(200, map[string]string{"id": other}), nil
		}
		return jsonResponse(401, map[string]string{}), nil
	}
	if token != "Bearer owner" && token != "Bearer fresh" {
		return jsonResponse(200, []Run{}), nil
	}
	switch r.URL.Path {
	case "/rest/v1/meeting_sessions":
		return jsonResponse(200, []map[string]string{{"id": meeting}}), nil
	case "/rest/v1/meeting_runs":
		switch r.Method {
		case "POST":
			var body struct {
				OccurrenceID string `json:"occurrence_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			db.run.OccurrenceID = body.OccurrenceID
			db.run.SaveTranscript = true
			db.run.TranscriptState = "saving"
			db.run.TranscriptExpiresAt = "2099-01-01T00:00:00Z"
		case "PATCH":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			db.run.Status = body["status"]
		case "GET":
			if id := r.URL.Query().Get("id"); id != "" && id != "eq."+db.run.ID {
				return jsonResponse(200, []Run{}), nil
			}
			if id := r.URL.Query().Get("meeting_id"); id != "" && id != "eq."+db.run.MeetingID {
				return jsonResponse(200, []Run{}), nil
			}
			if r.URL.Query().Get("save_transcript") != "" && (!db.run.SaveTranscript || db.run.TranscriptState == "deleted") {
				return jsonResponse(200, []Run{}), nil
			}
		}
		return jsonResponse(200, []Run{db.run}), nil
	case "/rest/v1/rpc/append_audio_chunk":
		db.run.ChunksReceived++
		return jsonResponse(200, []Run{db.run}), nil
	case "/rest/v1/rpc/stop_audio_run":
		db.run.Status = "stopped"
		return jsonResponse(200, []Run{db.run}), nil
	case "/rest/v1/rpc/append_transcript_final":
		var body struct {
			Seq      int     `json:"segment_seq"`
			Text     string  `json:"segment_text"`
			Start    float64 `json:"segment_start"`
			Duration float64 `json:"segment_duration"`
			Language string  `json:"segment_language"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		db.appends++
		db.tokens = append(db.tokens, token)
		if !db.run.SaveTranscript || db.run.TranscriptState == "deleted" || db.run.TranscriptExpiresAt < "2026" {
			return jsonResponse(200, []storedSegment{}), nil
		}
		segment := storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: body.Seq, Text: body.Text, Start: body.Start, Duration: body.Duration, Language: body.Language}
		if prior, exists := db.segments[body.Seq]; exists && prior != segment {
			return jsonResponse(409, map[string]string{}), nil
		}
		db.segments[body.Seq] = segment
		db.ack <- body.Seq
		return jsonResponse(200, []storedSegment{segment}), nil
	case "/rest/v1/rpc/set_transcript_state":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if db.run.TranscriptState != "saving" {
			return jsonResponse(200, []Run{}), nil
		}
		db.run.TranscriptState = body["state_value"]
		return jsonResponse(200, []Run{db.run}), nil
	case "/rest/v1/rpc/delete_transcript":
		db.run.TranscriptState = "deleted"
		clear(db.segments)
		return jsonResponse(200, []Run{db.run}), nil
	case "/rest/v1/transcript_segments":
		rows := []storedSegment{}
		for _, segment := range db.segments {
			allowed := true
			for _, filter := range r.URL.Query()["seq"] {
				parts := strings.SplitN(filter, ".", 2)
				n, _ := strconv.Atoi(parts[1])
				if parts[0] == "gt" && segment.Seq <= n || parts[0] == "lte" && segment.Seq > n {
					allowed = false
				}
			}
			if allowed {
				rows = append(rows, segment)
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if r.URL.Query().Get("order") == "seq.desc" {
				return rows[i].Seq > rows[j].Seq
			}
			return rows[i].Seq < rows[j].Seq
		})
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}
		return jsonResponse(200, rows), nil
	}
	return jsonResponse(404, map[string]string{}), nil
}

func TestArchiveStagingIsAutomaticAndLegacyConsentIsIgnoredByTestClient(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","occurrence_id":"`+occurrenceID+`","sample_rate":48000}`, "owner", "application/json")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	h.mu.Lock()
	controller := h.owners[runID]
	h.mu.Unlock()
	w = call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
	if w.Code != 200 {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if controller.writer != nil {
		waitWriter(t, controller.writer)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.appends != 1 || !db.run.SaveTranscript || db.run.OccurrenceID != occurrenceID {
		t.Fatalf("occurrence staging not enabled: appends=%d run=%+v", db.appends, db.run)
	}
}

func TestArchiveSavesBeyondBacklogAndSnapshotPages(t *testing.T) {
	h, db, stream := newArchiveHandler(t)
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	stream.results <- transcribe.Result{Text: "partial"}
	for i := range 251 {
		stream.results <- transcribe.Result{Text: fmt.Sprintf("final-%d", i), IsFinal: true}
		select {
		case <-db.ack:
		case <-time.After(time.Second):
			t.Fatal("final not persisted without subscriber")
		}
	}
	seen, after, through := 0, 0, -1
	for {
		path := fmt.Sprintf("/api/audio/runs/%s/segments?after_seq=%d&limit=50", runID, after)
		if through >= 0 {
			path += fmt.Sprintf("&through_seq=%d", through)
		}
		w := call(t, h, "GET", path, "", "owner", "")
		if w.Code != 200 {
			t.Fatalf("read: %d %s", w.Code, w.Body.String())
		}
		var page struct {
			Segments []transcribe.Segment `json:"segments"`
			Through  int                  `json:"through_seq"`
			Next     *int                 `json:"next_after_seq"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		through = page.Through
		seen += len(page.Segments)
		if page.Next == nil {
			break
		}
		after = *page.Next
	}
	if seen != 251 || through != 252 {
		t.Fatalf("archive truncated: count=%d snapshot=%d", seen, through)
	}
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments?through_seq=0", "", "owner", "")
	var empty struct {
		Segments []transcribe.Segment `json:"segments"`
		Through  int                  `json:"through_seq"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &empty)
	if w.Code != 200 || len(empty.Segments) != 0 || empty.Through != 0 {
		t.Fatalf("zero snapshot expanded: %s", w.Body.String())
	}
	call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
}

func TestArchiveAuthorizationUnavailableAndDeleteBarrier(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	for _, path := range []string{"not-uuid", unknownRun, runID} {
		w := call(t, h, "GET", "/api/audio/runs/"+path+"/segments", "", "other", "")
		if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("foreign read: %d %s", w.Code, w.Body.String())
		}
	}
	for _, state := range []string{"disabled", "deleted", "expired", "saving", "failed"} {
		db.mu.Lock()
		db.run.TranscriptState = state
		db.run.SaveTranscript = state != "disabled"
		db.run.TranscriptExpiresAt = "2099-01-01T00:00:00Z"
		if state == "expired" {
			db.run.TranscriptState = "saving"
			db.run.TranscriptExpiresAt = "2000-01-01T00:00:00Z"
		}
		db.mu.Unlock()
		w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
		var result struct {
			Storage struct {
				State    string `json:"state"`
				Complete bool   `json:"complete"`
			} `json:"storage"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		want := state
		if state == "saving" {
			want = "incomplete"
		}
		if w.Code != 200 || result.Storage.State != want || result.Storage.Complete {
			t.Fatalf("%s: %s", state, w.Body.String())
		}
	}
	for range 2 {
		if w := call(t, h, "DELETE", "/api/audio/runs/"+runID+"/segments", "", "owner", ""); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments?after_seq=-1", "", "owner", ""); w.Code != 400 {
		t.Fatal("negative cursor accepted")
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments?meeting_id="+other, "", "owner", ""); w.Code != 404 {
		t.Fatal("meeting mismatch accepted")
	}
}

// Stable tie-breaking and owner-bound cursors prevent duplicate/missing history.
func TestArchiveListStablePaginationAndCursorBinding(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	fixtures := []Run{db.run, db.run, db.run}
	fixtures[0].ID = unknownRun
	fixtures[1].ID = stoppedRun
	fixtures[2].ID = orphanRun
	fixtures[2].StartedAt = "2026-10-22T00:00:00Z"
	for i := range fixtures {
		fixtures[i].TranscriptExpiresAt = ""
	}
	h.repo.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/v1/meeting_sessions" {
			return jsonResponse(200, []map[string]string{{"id": meeting}}), nil
		}
		query := r.URL.Query()
		if query.Get("meeting_id") != "eq."+meeting || query.Get("order") != "started_at.desc,id.desc" || query.Get("save_transcript") != "eq.true" || query.Get("transcript_state") != "neq.deleted" || query.Has("transcript_expires_at") {
			return jsonResponse(500, map[string]string{}), nil
		}
		rows := fixtures
		position := query.Get("or")
		if position != "" {
			switch {
			case strings.Contains(position, "id.lt."+unknownRun):
				rows = fixtures[1:]
			case strings.Contains(position, "id.lt."+stoppedRun):
				rows = fixtures[2:]
			default:
				rows = nil
			}
		}
		limit, _ := strconv.Atoi(query.Get("limit"))
		if len(rows) > limit {
			rows = rows[:limit]
		}
		return jsonResponse(200, rows), nil
	})}
	cursor := ""
	for i, want := range []string{unknownRun, stoppedRun, orphanRun} {
		path := "/api/audio/runs?meeting_id=" + meeting + "&limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := call(t, h, "GET", path, "", "owner", "")
		var page struct {
			Runs []Run   `json:"runs"`
			Next *string `json:"next_cursor"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(page.Runs) != 1 || page.Runs[0].ID != want || page.Runs[0].Storage == nil || page.Runs[0].Storage.State != "incomplete" {
			t.Fatalf("page %d: %d %s", i, w.Code, w.Body.String())
		}
		if i < 2 {
			if page.Next == nil {
				t.Fatal("missing next cursor")
			}
			cursor = *page.Next
		} else if page.Next != nil {
			t.Fatal("spurious next cursor")
		}
	}
	if w := call(t, h, "GET", "/api/audio/runs?meeting_id="+meeting+"&cursor="+cursor, "", "other", ""); w.Code != 400 {
		t.Fatal("foreign owner cursor accepted")
	}
	for _, query := range []string{"limit=0", "limit=101", "cursor=bogus", "limit=1&limit=2"} {
		if w := call(t, h, "GET", "/api/audio/runs?meeting_id="+meeting+"&"+query, "", "owner", ""); w.Code != 400 {
			t.Fatalf("accepted invalid pagination %s", query)
		}
	}
}

func TestProviderFailureDrainsAcceptedFinalsSeparatelyFromCapture(t *testing.T) {
	h, db, stream := newArchiveHandler(t)
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	session, _ := h.broker.Session(runID)
	h.mu.Lock()
	writer := h.owners[runID].writer
	h.mu.Unlock()
	stream.results <- transcribe.Result{Text: "accepted before failure", IsFinal: true}
	select {
	case <-db.ack:
	case <-time.After(time.Second):
		t.Fatal("final not acknowledged")
	}
	stream.errors <- errors.New("private provider detail")
	select {
	case <-session.Ended():
	case <-time.After(time.Second):
		t.Fatal("provider did not terminate")
	}
	waitWriter(t, writer)
	if session.Err() == nil || writer.state() != "saved" {
		t.Fatalf("provider and storage states conflated: provider=%v storage=%s", session.Err(), writer.state())
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.segments) != 1 || db.run.TranscriptState != "saved" {
		t.Fatal("accepted final lost on provider failure")
	}
}

func TestChunkCredentialsOnlyAdvanceAfterVerifiedOwnerRequests(t *testing.T) {
	h, _, _ := newArchiveHandler(t)
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	h.mu.Lock()
	writer := h.owners[runID].writer
	h.mu.Unlock()
	verify := h.verify
	entered, release := make(chan struct{}), make(chan struct{})
	h.verify = func(ctx context.Context, token string) (string, *auth.VerificationError) {
		if token == "owner" {
			close(entered)
			<-release
		}
		return verify(ctx, token)
	}
	older := make(chan *httptest.ResponseRecorder, 1)
	go func() { older <- call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "old chunk", "owner", "") }()
	<-entered
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "fresh chunk", "fresh", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	close(release)
	if w := <-older; w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if writer.credential() != "fresh" {
		t.Fatal("older request overwrote refreshed credentials")
	}
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "foreign chunk", "other", ""); w.Code != 404 {
		t.Fatal("foreign request passed ownership")
	}
	if writer.credential() != "fresh" {
		t.Fatal("foreign request replaced owner credentials")
	}
	call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "fresh", "")
	waitWriter(t, writer)
}

func TestStopReturnsPendingWhileFinalWriteDrainsWithRefreshedToken(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	entered, release := make(chan struct{}), make(chan struct{})
	original := h.repo.client.Transport
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return original.RoundTrip(r)
	})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	h.mu.Lock()
	writer := h.owners[runID].writer
	h.mu.Unlock()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "fresh", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	<-entered
	var result struct {
		Storage storageMetadata `json:"storage"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.Storage.State != "saving" || result.Storage.Complete {
		t.Fatalf("stop falsely acknowledged drain: %s", w.Body.String())
	}
	h.mu.Lock()
	_, retained := h.owners[runID]
	h.mu.Unlock()
	if !retained {
		t.Fatal("owner dropped before final acknowledgment")
	}
	close(release)
	waitWriter(t, writer)
	db.mu.Lock()
	defer db.mu.Unlock()
	if writer.state() != "saved" || len(db.tokens) != 1 || db.tokens[0] != "Bearer fresh" {
		t.Fatalf("final lost refreshed credentials: state=%s tokens=%v", writer.state(), db.tokens)
	}
}

func TestDeleteStopsFuturePersistenceWithoutStoppingLiveSession(t *testing.T) {
	h, db, stream := newArchiveHandler(t)
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	session, _ := h.broker.Session(runID)
	h.mu.Lock()
	writer := h.owners[runID].writer
	h.mu.Unlock()
	stream.results <- transcribe.Result{Text: "saved first", IsFinal: true}
	select {
	case <-db.ack:
	case <-time.After(time.Second):
		t.Fatal("first final not stored")
	}
	for range 2 {
		if w := call(t, h, "DELETE", "/api/audio/runs/"+runID+"/segments", "", "owner", ""); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	waitWriter(t, writer)
	segments, unsubscribe := session.Subscribe(1)
	defer unsubscribe()
	stream.results <- transcribe.Result{Text: "still live", IsFinal: true}
	select {
	case result := <-segments:
		if result.Text != "still live" {
			t.Fatal("wrong live result")
		}
	case <-time.After(time.Second):
		t.Fatal("delete stopped live transcript")
	}
	if err := session.Write([]byte{1, 2}); err != nil {
		t.Fatal("delete closed audio provider")
	}
	db.mu.Lock()
	if len(db.segments) != 0 || db.appends != 1 || db.run.TranscriptState != "deleted" {
		t.Fatal("delete barrier failed")
	}
	db.mu.Unlock()
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", ""); w.Code != 200 || strings.Contains(w.Body.String(), "saved first") {
		t.Fatal("deleted text remained readable")
	}
	call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
}

func TestArchiveRepositoryPreservesImmutableSequenceIdentity(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	segment := transcribe.Segment{Seq: 2, Type: "final", Text: "original", Start: 1.5, Duration: 0.2, Language: "id"}
	accepted, err := h.repo.appendFinal(context.Background(), db.run, segment, "owner")
	if err != nil || !accepted {
		t.Fatalf("first final: %v", err)
	}
	accepted, err = h.repo.appendFinal(context.Background(), db.run, segment, "owner")
	if err != nil || !accepted {
		t.Fatalf("identical duplicate: %v", err)
	}
	segment.Text = "replacement"
	if _, err = h.repo.appendFinal(context.Background(), db.run, segment, "owner"); err == nil {
		t.Fatal("conflicting final was accepted")
	}
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	var result struct {
		Segments []transcribe.Segment `json:"segments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || len(result.Segments) != 1 || result.Segments[0].Seq != 2 || result.Segments[0].Text != "original" {
		t.Fatalf("canonical final mutated: %s", w.Body.String())
	}
}

func TestArchiveRejectsCorruptReturnedIdentityAndStorageMetadata(t *testing.T) {
	for _, mutation := range []string{"owner", "run", "meeting", "expiry", "state"} {
		t.Run(mutation, func(t *testing.T) {
			h, db, _ := newArchiveHandler(t)
			run := db.run
			switch mutation {
			case "owner":
				run.UserID = other
			case "run":
				run.ID = unknownRun
			case "meeting":
				run.MeetingID = "invalid"
			case "expiry":
				run.TranscriptExpiresAt = ""
			case "state":
				run.TranscriptState = "unknown"
			}
			h.repo.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return jsonResponse(200, []Run{run}), nil })}
			if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", ""); w.Code != 502 {
				t.Fatalf("corrupt %s accepted: %d %s", mutation, w.Code, w.Body.String())
			}
		})
	}
}

func TestDeleteDisabledArchiveReportsTerminalDelete(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	db.run.SaveTranscript = false
	db.run.TranscriptState = "disabled"
	db.run.TranscriptExpiresAt = ""
	w := call(t, h, "DELETE", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	var result struct {
		Storage storageMetadata `json:"storage"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Storage.State != "deleted" || result.Storage.Complete {
		t.Fatalf("empty archive did not delete: %s", w.Body.String())
	}
}

func TestArchiveExpiryBoundaryHidesTextWithoutDeletingCapture(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	boundary := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	db.run.TranscriptExpiresAt = nullableTimestamp(boundary.Format(time.RFC3339Nano))
	db.run.TranscriptState = "saved"
	db.segments[1] = storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: 1, Text: "retained until expiry"}
	h.now = func() time.Time { return boundary.Add(-time.Nanosecond) }
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "retained until expiry") {
		t.Fatal("archive expired before boundary")
	}
	h.now = func() time.Time { return boundary }
	w = call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	var result struct {
		Run      Run                  `json:"run"`
		Storage  storageMetadata      `json:"storage"`
		Segments []transcribe.Segment `json:"segments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Storage.State != "expired" || result.Storage.Complete || len(result.Segments) != 0 || result.Run.ID != runID {
		t.Fatalf("expiry leaked text or capture identity: %s", w.Body.String())
	}
}

func TestArchiveOversizedDataResponseFailsExplicitly(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	original := h.repo.client.Transport
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/v1/transcript_segments" {
			return jsonResponse(200, []storedSegment{{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: 1, Text: strings.Repeat("x", 9<<20)}}), nil
		}
		return original.RoundTrip(r)
	})
	db.run.TranscriptState = "saved"
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	if w.Code != 502 || strings.Contains(w.Body.String(), `"segments"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("oversized archive was silently truncated: %d", w.Code)
	}
}
