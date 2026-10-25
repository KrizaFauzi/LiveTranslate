package assistance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/sumopod"
	"livetranslate/backend/internal/transcribe"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestQuestionDetectionIsBounded(t *testing.T) {
	for _, s := range []string{"What is the deadline?", "bagaimana prosesnya", "Apakah tersedia"} {
		if !isQuestion(s) {
			t.Fatalf("question missed: %s", s)
		}
	}
	if isQuestion("This is a status update.") {
		t.Fatal("statement detected")
	}
}

func TestSplitQuestionsCreatesOneItemPerQuestion(t *testing.T) {
	got, overflow := splitQuestions("One? Two? Three? Four? Five? Six?")
	if overflow || len(got) != 6 || got[0] != "One?" || got[5] != "Six?" {
		t.Fatalf("questions were not completely split: %#v overflow=%v", got, overflow)
	}
	_, overflow = splitQuestions(strings.Repeat("Question? ", maxQuestionsPerSegment+1))
	if !overflow {
		t.Fatal("oversized question batch was not reported")
	}
}
func TestDraftRequiresValidEvidenceCitationAndRevisesOnce(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		content := `not json`
		switch calls {
		case 2:
			content = `{"id":"Batas waktunya Jumat.","en":"The deadline is Friday.","citations":[{"source":1,"verbatim":"Deadline Friday","supports_id":true,"supports_en":true}]}`
		case 3:
			content = `{"supported":true,"grammar":true}`
		}
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	provider := sumopod.NewClient(config.Config{SumopodBaseURL: "https://ai.sumopod.com/v1", SumopodKey: "key", SumopodModel: "model"}, client)
	id, en, citations, revision, err := draft(context.Background(), provider, "Kapan?", []Evidence{{DocumentID: "doc", FileName: "brief.csv", DocumentVersion: 1, ChunkIndex: 0, Locator: map[string]any{"row": 1}, Text: "Deadline Friday"}})
	if err != nil || id == "" || en == "" || len(citations) != 1 || citations[0]["verbatim_text"] != "Deadline Friday" || revision != 1 || calls != 3 {
		t.Fatalf("unexpected result: %q %q %+v rev=%d calls=%d err=%v", id, en, citations, revision, calls, err)
	}
}

func TestStatementIsMarkedProcessedWithoutCallingProvider(t *testing.T) {
	var statusBody map[string]any
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/v1/rpc/complete_assistance_segment" {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&statusBody); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`[]`)), Header: http.Header{}}, nil
	})}
	worker := &Worker{
		repo:  newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		run:   Run{ID: "run", UserID: "user"},
		token: "jwt",
	}
	worker.process(transcribe.Segment{Type: "final", Seq: 9, Text: "This is a status update."})
	if statusBody["result_status"] != "completed" || statusBody["segment_seq"] != float64(9) {
		t.Fatalf("unexpected status call: %+v", statusBody)
	}
}

func TestAssistanceQueueOverflowNeverBlocksTranscriptPersistence(t *testing.T) {
	var databaseCalls atomic.Int32
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		databaseCalls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(`[]`)), Header: http.Header{}}, nil
	})}
	worker := &Worker{
		repo:  newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		run:   Run{ID: "run", UserID: "user"},
		queue: make(chan transcribe.Segment, 1),
		token: "jwt",
	}
	worker.queue <- transcribe.Segment{Type: "final", Seq: 1}
	returned := make(chan struct{})
	go func() {
		worker.Enqueue(transcribe.Segment{Type: "final", Seq: 2})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("full AI queue blocked transcript persistence")
	}
	if !worker.overflow.Load() {
		t.Fatal("overflow was not retained for terminal reconciliation")
	}
	if databaseCalls.Load() != 0 {
		t.Fatal("queue overflow performed database I/O in transcript callback")
	}
}

func TestDraftAcceptsExplicitNoEvidenceWithRetrievedChunks(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"id":"-","en":"-","citations":[]}`}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	provider := sumopod.NewClient(config.Config{SumopodBaseURL: "https://ai.sumopod.com/v1", SumopodKey: "key", SumopodModel: "model"}, client)
	id, en, citations, revision, err := draft(context.Background(), provider, "Berapa anggarannya?", []Evidence{{Text: "Dokumen tidak menyebut anggaran."}})
	if err != nil || id != "-" || en != "-" || len(citations) != 0 || revision != 0 || calls != 1 {
		t.Fatalf("unexpected abstention: %q %q %+v rev=%d calls=%d err=%v", id, en, citations, revision, calls, err)
	}
}
