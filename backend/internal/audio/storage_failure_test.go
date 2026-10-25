package audio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"livetranslate/backend/internal/transcribe"
)

func TestStorageAuthorizationFailureLeavesLiveTranscriptionHealthy(t *testing.T) {
	h, db, stream := newArchiveHandler(t)
	original := h.repo.client.Transport
	h.repo.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/append_transcript_final") {
			return jsonResponse(401, map[string]string{}), nil
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
	stream.results <- transcribe.Result{Text: "cannot save", IsFinal: true}
	waitWriter(t, writer)
	segments, unsubscribe := session.Subscribe(1)
	defer unsubscribe()
	stream.results <- transcribe.Result{Text: "live keeps working"}
	select {
	case segment := <-segments:
		if segment.Text != "live keeps working" {
			t.Fatal("wrong live segment")
		}
	case <-time.After(time.Second):
		t.Fatal("storage failure stopped live publication")
	}
	if session.Err() != nil || session.Write([]byte{1}) != nil {
		t.Fatal("storage failure became provider failure")
	}
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/segments", "", "owner", "")
	var result struct {
		Storage storageMetadata `json:"storage"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Storage.State != "failed" || result.Storage.Complete {
		t.Fatalf("missing storage failure: %s", w.Body.String())
	}
	db.mu.Lock()
	status := db.run.Status
	db.mu.Unlock()
	if status != "active" {
		t.Fatal("storage failure closed capture metadata")
	}
	call(t, h, "POST", "/api/audio/runs/"+runID+"/stop", "", "owner", "")
}
