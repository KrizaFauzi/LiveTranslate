package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
)

type gatedFinalizeStream struct {
	*fakeStream
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gatedFinalizeStream) Finalize() error {
	close(s.started)
	<-s.release
	return s.fakeStream.Finalize()
}

func (s *gatedFinalizeStream) allowFinal() { s.once.Do(func() { close(s.release) }) }

type archiveTestProvider struct{ stream transcribe.Stream }

func (p archiveTestProvider) Open(context.Context, int) (transcribe.Stream, error) {
	return p.stream, nil
}

func gatedArchiveHandler(t *testing.T) (*handler, *archiveDB, *gatedFinalizeStream) {
	t.Helper()
	h, db, stream := newArchiveHandler(t)
	gated := &gatedFinalizeStream{fakeStream: stream, started: make(chan struct{}), release: make(chan struct{})}
	h.broker = transcribe.NewBroker(config.Config{}, archiveTestProvider{stream: gated})
	h.broker.OnProviderEnd(h.markRunFailed)
	t.Cleanup(gated.allowFinal)
	return h, db, gated
}

// A missing broker entry after the bounded stop response is not proof that the
// provider has stopped publishing: its finalization still owns the final sink.
func TestMissingBrokerChunkDoesNotFinishPendingStopArchive(t *testing.T) {
	previous := closeBudget
	closeBudget = 0
	defer func() { closeBudget = previous }()
	h, db, stream := gatedArchiveHandler(t)
	original := h.repo.client.Transport
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/stop_audio_run") {
			return jsonResponse(500, map[string]string{}), nil
		}
		return original.RoundTrip(r)
	})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	session, _ := h.broker.Session(runID)
	h.mu.Lock()
	writer := h.owners[runID].writer
	h.mu.Unlock()
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", ""); w.Code != 502 {
		t.Fatalf("stop did not fail bookkeeping: %d %s", w.Code, w.Body.String())
	}
	<-stream.started
	if w := call(t, h, "POST", "/api/audio/runs/"+runID+"/chunks", "late chunk", "owner", ""); w.Code != 409 {
		t.Fatalf("late chunk: %d %s", w.Code, w.Body.String())
	}
	writer.mu.Lock()
	premature := writer.finished
	writer.mu.Unlock()
	if premature {
		t.Fatal("missing broker entry prematurely finished the final sink")
	}
	h.mu.Lock()
	_, retained := h.owners[runID]
	h.mu.Unlock()
	if !retained {
		t.Fatal("owner dropped while provider finalization still pending")
	}
	stream.allowFinal()
	select {
	case <-session.Ended():
	case <-time.After(time.Second):
		t.Fatal("provider finalization did not finish")
	}
	waitWriter(t, writer)
	db.mu.Lock()
	defer db.mu.Unlock()
	if writer.state() != "saved" || len(db.segments) != 1 || db.segments[1].Text != "selamat pagi" {
		t.Fatal("late provider final was not durably drained")
	}
}

func TestArchiveFrozenCeilingNeverClaimsLaterCompletion(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		initial  int
		active   bool
		explicit bool
		ceiling  int
	}{
		{"active_zero", 0, true, false, 0},
		{"active_nonzero", 1, true, false, 1},
		{"saved_old_ceiling", 2, false, true, 1},
		{"saved_zero_ceiling", 1, false, true, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h, db, _ := newArchiveHandler(t)
			if !scenario.active {
				db.run.TranscriptState = "saved"
			}
			for seq := 1; seq <= scenario.initial; seq++ {
				db.segments[seq] = storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: seq, Text: "before freeze"}
			}
			original := h.repo.client.Transport
			metadataReads := 0
			h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "GET" {
					metadataReads++
					if metadataReads == 2 && scenario.active {
						db.mu.Lock()
						seq := scenario.initial + 1
						db.segments[seq] = storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: seq, Text: "after freeze"}
						db.run.TranscriptState = "saved"
						db.mu.Unlock()
					}
				}
				return original.RoundTrip(r)
			})
			path := "/api/audio/runs/" + runID + "/segments"
			if scenario.explicit {
				path += "?through_seq=" + strconv.Itoa(scenario.ceiling)
			}
			w := call(t, h, "GET", path, "", "owner", "")
			var result struct {
				Storage  storageMetadata      `json:"storage"`
				Through  int                  `json:"through_seq"`
				Segments []transcribe.Segment `json:"segments"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || result.Through != scenario.ceiling || result.Storage.Complete || strings.Contains(w.Body.String(), "after freeze") {
				t.Fatalf("frozen snapshot falsely complete: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestStopResponseUsesAcknowledgedArchiveAfterOwnerCleanup(t *testing.T) {
	h, db, stream := gatedArchiveHandler(t)
	original := h.repo.client.Transport
	var writer *transcriptWriter
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/stop_audio_run") {
			db.mu.Lock()
			db.run.Status = "stopped"
			stale := db.run
			db.mu.Unlock()
			<-stream.started
			stream.allowFinal()
			waitWriter(t, writer)
			// Force the legal cleanup ordering before releasing the stale RPC row.
			h.dropOwner(runID)
			return jsonResponse(200, []Run{stale}), nil
		}
		return original.RoundTrip(r)
	})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	h.mu.Lock()
	writer = h.owners[runID].writer
	h.mu.Unlock()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
	var result struct {
		Storage storageMetadata `json:"storage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Storage.State != "saved" || !result.Storage.Complete {
		t.Fatalf("acknowledged archive reported incomplete: %d %s", w.Code, w.Body.String())
	}
}

func TestProviderOpenFailureTerminatesStorageWriter(t *testing.T) {
	h, _, _ := newArchiveHandler(t)
	h.broker = transcribe.NewBroker(config.Config{}, failingProvider{})
	h.broker.OnProviderEnd(h.markRunFailed)
	original := h.repo.client.Transport
	captured := make(chan *transcriptWriter, 1)
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/set_transcript_state") {
			h.mu.Lock()
			writer := h.owners[runID].writer
			h.mu.Unlock()
			captured <- writer
		}
		return original.RoundTrip(r)
	})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 502 {
		t.Fatalf("provider open unexpectedly succeeded: %d", w.Code)
	}
	select {
	case writer := <-captured:
		if writer == nil {
			t.Fatal("failed writer lost ownership before failure bookkeeping")
		}
		waitWriter(t, writer)
		if writer.state() != "failed" {
			t.Fatalf("failed provider open left storage %s", writer.state())
		}
	case <-time.After(time.Second):
		t.Fatal("failed provider open did not terminate its writer")
	}
}

func TestCompletedArchiveCeilingCanStillProveComplete(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			h, db, _ := newArchiveHandler(t)
			db.run.TranscriptState = "saved"
			for seq := 1; seq <= count; seq++ {
				db.segments[seq] = storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: seq, Text: "completed final"}
			}
			w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments?through_seq="+strconv.Itoa(count), "", "owner", "")
			var result struct {
				Storage  storageMetadata      `json:"storage"`
				Through  int                  `json:"through_seq"`
				Segments []transcribe.Segment `json:"segments"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || result.Storage.State != "saved" || !result.Storage.Complete || result.Through != count || len(result.Segments) != count {
				t.Fatalf("complete snapshot lost acknowledgment: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestStopMetadataRefreshPreservesDeletedArchive(t *testing.T) {
	h, db, stream := gatedArchiveHandler(t)
	original := h.repo.client.Transport
	var writer *transcriptWriter
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/stop_audio_run") {
			db.mu.Lock()
			db.run.Status = "stopped"
			stale := db.run
			db.mu.Unlock()
			<-stream.started
			stream.allowFinal()
			waitWriter(t, writer)
			h.dropOwner(runID)
			db.mu.Lock()
			db.run.TranscriptState = "deleted"
			clear(db.segments)
			db.mu.Unlock()
			return jsonResponse(200, []Run{stale}), nil
		}
		return original.RoundTrip(r)
	})
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	h.mu.Lock()
	writer = h.owners[runID].writer
	h.mu.Unlock()
	w := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
	var result struct {
		Storage storageMetadata `json:"storage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Storage.State != "deleted" || result.Storage.Complete {
		t.Fatalf("terminal delete was overwritten by stale stop metadata: %d %s", w.Code, w.Body.String())
	}
}

func TestSnapshotCompletionProofCannotHideConcurrentDelete(t *testing.T) {
	h, db, _ := newArchiveHandler(t)
	db.run.TranscriptState = "saved"
	for seq := 1; seq <= 2; seq++ {
		db.segments[seq] = storedSegment{ID: runID, UserID: owner, MeetingID: meeting, RunID: runID, Seq: seq, Text: "before deletion"}
	}
	original := h.repo.client.Transport
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/v1/transcript_segments" && r.URL.Query().Get("order") == "seq.desc" {
			db.mu.Lock()
			db.run.TranscriptState = "deleted"
			clear(db.segments)
			db.mu.Unlock()
		}
		return original.RoundTrip(r)
	})
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments?through_seq=1", "", "owner", "")
	var result struct {
		Storage  storageMetadata      `json:"storage"`
		Segments []transcribe.Segment `json:"segments"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Storage.State != "deleted" || result.Storage.Complete || len(result.Segments) != 0 {
		t.Fatalf("snapshot proof hid terminal delete: %d %s", w.Code, w.Body.String())
	}
}

func TestStopMetadataRefreshFailurePreservesAcknowledgedStop(t *testing.T) {
	previousClose, previousMetadata := closeBudget, stopMetadataBudget
	closeBudget, stopMetadataBudget = 0, time.Millisecond
	defer func() { closeBudget, stopMetadataBudget = previousClose, previousMetadata }()
	for _, unavailable := range []string{"timeout", "missing"} {
		t.Run(unavailable, func(t *testing.T) {
			h, db, stream := gatedArchiveHandler(t)
			original := h.repo.client.Transport
			acknowledged := make(chan struct{})
			h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/stop_audio_run") {
					response, err := original.RoundTrip(r)
					close(acknowledged)
					return response, err
				}
				if r.URL.Path == "/rest/v1/meeting_runs" && r.Method == "GET" {
					select {
					case <-acknowledged:
						if unavailable == "missing" {
							return jsonResponse(200, []Run{}), nil
						}
						<-r.Context().Done()
						return nil, r.Context().Err()
					default:
					}
				}
				return original.RoundTrip(r)
			})
			if response := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000,"save_transcript":true}`, "owner", "application/json"); response.Code != 201 {
				t.Fatal(response.Body.String())
			}
			h.mu.Lock()
			writer := h.owners[runID].writer
			h.mu.Unlock()
			response := call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
			var result struct {
				Run     Run             `json:"run"`
				Storage storageMetadata `json:"storage"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || result.Run.Status != "stopped" || result.Storage.Complete {
				t.Fatalf("auxiliary metadata invalidated acknowledged stop: %d %s", response.Code, response.Body.String())
			}
			stream.allowFinal()
			waitWriter(t, writer)
			db.mu.Lock()
			defer db.mu.Unlock()
			if len(db.segments) != 1 || db.segments[1].Text != "selamat pagi" {
				t.Fatal("late final was lost after metadata refresh failure")
			}
		})
	}
}
