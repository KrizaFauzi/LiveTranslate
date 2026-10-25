package audio

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
)

func waitWriter(t *testing.T, writer *transcriptWriter) {
	t.Helper()
	select {
	case <-writer.done:
	case <-time.After(time.Second):
		t.Fatal("writer did not finish")
	}
}

// A failed append must not become a saved archive, nor be automatically retried.
func TestWriterFailureDoesNotClaimSavedOrRetry(t *testing.T) {
	var mu sync.Mutex
	appends, saved := 0, 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			appends++
			return jsonResponse(401, map[string]string{}), nil
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["state_value"] == "saved" {
			saved++
		}
		return jsonResponse(200, []Run{}), nil
	})}
	writer := newTranscriptWriter(newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client), Run{ID: runID, UserID: owner, MeetingID: meeting}, "old", 1)
	writer.Enqueue(transcribe.Segment{Seq: 2, Type: "final", Text: "one"})
	writer.Enqueue(transcribe.Segment{Seq: 4, Type: "final", Text: "two"})
	writer.Finish()
	waitWriter(t, writer)
	mu.Lock()
	defer mu.Unlock()
	if appends != 1 || saved != 0 || writer.state() != "failed" {
		t.Fatalf("appends=%d saved=%d state=%s", appends, saved, writer.state())
	}
}

// An older verified request cannot overwrite newer credentials while stop drains.
func TestWriterCredentialTicketsAndFinalDrain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var tokens []string
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			tokens = append(tokens, r.Header.Get("Authorization"))
			if len(tokens) == 1 {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			var body struct {
				Seq  int    `json:"segment_seq"`
				Text string `json:"segment_text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			return jsonResponse(200, []storedSegment{{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: body.Seq, Text: body.Text}}), nil
		}
		return jsonResponse(200, []Run{{ID: runID, UserID: owner, MeetingID: meeting, OccurrenceID: occurrenceID, SaveTranscript: true, TranscriptState: "saved", TranscriptExpiresAt: "2099-01-01T00:00:00Z"}}), nil
	})}
	writer := newTranscriptWriter(newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client), Run{ID: runID, UserID: owner, MeetingID: meeting}, "old", 1)
	writer.Enqueue(transcribe.Segment{Seq: 1, Type: "final", Text: "one"})
	<-entered
	writer.refresh(owner, "new", 3)
	writer.refresh(owner, "stale", 2)
	writer.refresh(other, "foreign", 4)
	writer.beginDrain()
	writer.Enqueue(transcribe.Segment{Seq: 3, Type: "final", Text: "stop final"})
	writer.Finish()
	close(release)
	waitWriter(t, writer)
	if len(tokens) != 2 || tokens[0] != "Bearer old" || tokens[1] != "Bearer new" || writer.state() != "saved" {
		t.Fatalf("tokens=%v state=%s", tokens, writer.state())
	}
}

func TestWriterBoundedQueueFailsWithoutBlockingLive(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return jsonResponse(200, []storedSegment{{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: 1, Text: "first"}}), nil
		}
		return jsonResponse(200, []Run{}), nil
	})}
	writer := newTranscriptWriter(newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client), Run{ID: runID, UserID: owner, MeetingID: meeting}, "owner", 1)
	writer.Enqueue(transcribe.Segment{Seq: 1, Type: "final", Text: "first"})
	<-entered
	for seq := 2; seq <= 130; seq++ {
		writer.Enqueue(transcribe.Segment{Seq: seq, Type: "final", Text: "queued"})
	}
	if writer.state() != "failed" {
		t.Fatal("overflow was not observable")
	}
	writer.Finish()
	close(release)
	waitWriter(t, writer)
}

func TestWriterRejectsOversizedFinalAndDrainTimeout(t *testing.T) {
	for _, oversized := range []bool{true, false} {
		t.Run(map[bool]string{true: "bytes", false: "deadline"}[oversized], func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return jsonResponse(200, []Run{}), nil
			})}
			writer := newTranscriptWriter(newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client), Run{ID: runID, UserID: owner, MeetingID: meeting}, "owner", 1)
			text := "final"
			if oversized {
				text = strings.Repeat("a", (2<<20)+1)
			}
			writer.Enqueue(transcribe.Segment{Seq: 1, Type: "final", Text: text})
			if !oversized {
				writer.cancel()
			}
			writer.Finish()
			waitWriter(t, writer)
			if writer.state() != "failed" {
				t.Fatal("failure was reported saved")
			}
		})
	}
}

func TestWriterSkippedAppendCannotBeSaved(t *testing.T) {
	for _, terminal := range []string{"deleted", "expired", "disabled"} {
		t.Run(terminal, func(t *testing.T) {
			h, db, _ := newArchiveHandler(t)
			db.mu.Lock()
			db.run.TranscriptState = terminal
			if terminal == "disabled" {
				db.run.SaveTranscript = false
			}
			if terminal == "expired" {
				db.run.TranscriptState = "saving"
				db.run.TranscriptExpiresAt = "2000-01-01T00:00:00Z"
			}
			run := db.run
			db.mu.Unlock()
			writer := newTranscriptWriter(h.repo, run, "owner", 1)
			writer.Enqueue(transcribe.Segment{Seq: 1, Type: "final", Text: "never acknowledged"})
			writer.Finish()
			waitWriter(t, writer)
			if writer.state() != terminal {
				t.Fatalf("skipped write became %s", writer.state())
			}
			db.mu.Lock()
			defer db.mu.Unlock()
			if len(db.segments) != 0 || db.run.TranscriptState == "saved" {
				t.Fatal("terminal archive revived")
			}
		})
	}
}

func TestWriterDrainDeadlineCancelsInflightRequest(t *testing.T) {
	var clockMu sync.Mutex
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	var deadline time.Time
	var expire func()
	clockNow := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	clockAfter := func(delay time.Duration, fn func()) *time.Timer {
		clockMu.Lock()
		defer clockMu.Unlock()
		deadline = now.Add(delay)
		expire = fn
		return nil
	}
	advance := func(delta time.Duration) {
		clockMu.Lock()
		now = now.Add(delta)
		due := !now.Before(deadline)
		fn := expire
		clockMu.Unlock()
		if due && fn != nil {
			fn()
		}
	}
	entered, canceled := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			return jsonResponse(200, []Run{}), nil
		}
		actualDeadline, ok := r.Context().Deadline()
		if !ok || time.Until(actualDeadline) > 10*time.Second {
			t.Error("write did not have bounded request deadline")
		}
		close(entered)
		<-r.Context().Done()
		close(canceled)
		return nil, r.Context().Err()
	})}
	writer := startTranscriptWriter(newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client), Run{ID: runID, UserID: owner, MeetingID: meeting}, "owner", 1, clockNow, clockAfter)
	writer.Enqueue(transcribe.Segment{Seq: 1, Type: "final", Text: "pending"})
	<-entered
	writer.beginDrain()
	writer.Finish()
	advance(29 * time.Second)
	if writer.state() != "saving" {
		t.Fatal("drain deadline ended early")
	}
	advance(time.Second)
	waitWriter(t, writer)
	select {
	case <-canceled:
	default:
		t.Fatal("deadline left database request alive")
	}
	if writer.state() != "failed" {
		t.Fatal("deadline was falsely saved")
	}
}
