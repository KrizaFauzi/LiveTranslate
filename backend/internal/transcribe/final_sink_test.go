package transcribe

import (
	"context"
	"sync"
	"testing"
	"time"

	"livetranslate/backend/internal/config"
)

type recordingFinalSink struct {
	mu       sync.Mutex
	segments []Segment
	finished int
	done     chan struct{}
}

func (s *recordingFinalSink) Enqueue(segment Segment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.segments = append(s.segments, segment)
}
func (s *recordingFinalSink) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished++
	if s.finished == 1 {
		close(s.done)
	}
}

// Removing the publication sink, moving it to Subscribe, or renumbering finals
// loses the early final, the partial gap, or the results beyond the backlog.
func TestFinalSinkReceivesEarlyFinalsWithoutSubscribers(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 512), errors: make(chan error)}
	stream.results <- Result{Text: "partial"}
	for range 250 {
		stream.results <- Result{Text: "final", IsFinal: true}
	}
	close(stream.results)
	close(stream.errors)
	sink := &recordingFinalSink{done: make(chan struct{})}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run", 48000, sink)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.done:
	case <-time.After(time.Second):
		t.Fatal("sink was not finished")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.segments) != 250 || sink.segments[0].Seq != 2 || sink.segments[249].Seq != 251 {
		t.Fatalf("finals lost or resequenced: %+v", sink.segments)
	}
	if sink.finished != 1 || len(session.Segments()) != 100 {
		t.Fatal("incorrect terminal publication")
	}
}

func TestFinalSinkFinishesAfterStopFinal(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error), finalText: "last final"}
	sink := &recordingFinalSink{done: make(chan struct{})}
	session := NewSession(stream, nil, sink)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.finished != 1 || len(sink.segments) != 1 || sink.segments[0].Text != "last final" {
		t.Fatalf("stop lost its final: %+v, finishes=%d", sink.segments, sink.finished)
	}
}
