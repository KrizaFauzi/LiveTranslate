package audio

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"livetranslate/backend/internal/transcribe"
)

func TestTranslationQueueOverflowNeverBlocksTranscriptPersistence(t *testing.T) {
	base, _ := url.Parse("https://project.supabase.co")
	called := make(chan struct{}, 1)
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		called <- struct{}{}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}
	worker := &translationWorker{
		repo:  repository{base: base, key: "sb_publishable_test", client: client},
		queue: make(chan transcribe.Segment, 1),
	}
	worker.queue <- transcribe.Segment{Type: "final", Seq: 1, Text: "first"}
	done := make(chan struct{})
	go func() {
		worker.enqueue(transcribe.Segment{Type: "final", Seq: 2, Text: "second"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("full AI queue blocked transcript persistence")
	}
	if !worker.overflow.Load() {
		t.Fatal("overflow was not retained for terminal reconciliation")
	}
	select {
	case <-called:
		t.Fatal("queue overflow performed database I/O in the transcript callback")
	default:
	}
}
