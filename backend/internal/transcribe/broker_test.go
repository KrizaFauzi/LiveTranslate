package transcribe

import (
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"testing"
	"time"
)

type fakeStream struct {
	writes  [][]byte
	results chan Result
	errors  chan error
	closed  bool
}

func (s *fakeStream) Write(payload []byte) error { s.writes = append(s.writes, payload); return nil }

// Finalize publishes the final transcript and ends the provider stream, like the
// real provider that flushes the pending transcript and then closes the connection.
func (s *fakeStream) Finalize() error {
	select {
	case s.results <- Result{Text: "selamat pagi", IsFinal: true}:
	default:
	}
	close(s.results)
	return nil
}
func (s *fakeStream) Close() error           { s.closed = true; return nil }
func (s *fakeStream) Results() <-chan Result { return s.results }
func (s *fakeStream) Errors() <-chan error   { return s.errors }

type fakeProvider struct {
	stream *fakeStream
	err    error
}

func (p *fakeProvider) Open(context.Context, int) (Stream, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.stream, nil
}

func TestBrokerPublishesSegmentsInOrder(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	stream.results <- Result{Text: "halo", IsFinal: false}
	stream.results <- Result{Text: "halo dunia", IsFinal: true}
	first := <-segments
	second := <-segments
	if first.Seq != 1 || first.Type != "partial" || second.Seq != 2 || second.Type != "final" {
		t.Fatalf("unexpected segments: %+v %+v", first, second)
	}
	if len(session.Segments()) != 2 {
		t.Fatalf("unexpected backlog: %d", len(session.Segments()))
	}
}

func TestBrokerReplaysBacklogSince(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, _ := broker.Open(context.Background(), "run-1", 48000)
	stream.results <- Result{Text: "satu", IsFinal: true}
	stream.results <- Result{Text: "dua", IsFinal: true}
	segments, unsubscribe := session.Subscribe(1)
	defer unsubscribe()
	replayed := <-segments
	if replayed.Seq != 2 || replayed.Text != "dua" {
		t.Fatalf("unexpected replay: %+v", replayed)
	}
}

func TestBrokerOpenFails(t *testing.T) {
	broker := NewBroker(config.Config{}, &fakeProvider{err: errors.New("gagal")})
	if _, err := broker.Open(context.Background(), "run-1", 48000); err == nil {
		t.Fatal("expected error")
	}
}

func TestBrokerSkipsEmptyResults(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	stream.results <- Result{Text: "   "}
	stream.results <- Result{Text: "halo"}
	segment := <-segments
	if segment.Seq != 1 || segment.Text != "halo" {
		t.Fatalf("unexpected segment: %+v", segment)
	}
	if len(session.Segments()) != 1 {
		t.Fatalf("unexpected backlog: %d", len(session.Segments()))
	}
}

func TestProviderFailureClosesSubscribers(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	stream.errors <- errors.New("transkripsi Deepgram gagal")
	select {
	case _, ok := <-segments:
		if ok {
			t.Fatal("expected the subscriber channel to close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the subscriber channel stayed open after a provider failure")
	}
}

func TestSlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	_, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for range 300 {
			stream.results <- Result{Text: "halo", IsFinal: true}
		}
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the publisher blocked on a slow subscriber")
	}
}

func TestSessionWriteReachesProvider(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if len(stream.writes) != 1 {
		t.Fatalf("unexpected writes: %d", len(stream.writes))
	}
}

func TestCloseFlushesTheFinalSegment(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	final := <-segments
	if final.Type != "final" || final.Text != "selamat pagi" {
		t.Fatalf("unexpected final segment: %+v", final)
	}
	if _, ok := <-segments; ok {
		t.Fatal("expected the subscriber channel to close")
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	unsubscribe()
	for {
		if _, ok := <-segments; !ok {
			break
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	unsubscribe()
	unsubscribe()
	if _, ok := <-segments; ok {
		t.Fatal("expected the subscriber channel to close")
	}
	stream.results <- Result{Text: "halo", IsFinal: true}
	deadline := time.Now().Add(2 * time.Second)
	for len(session.Segments()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(session.Segments()) != 1 {
		t.Fatalf("unexpected backlog: %d", len(session.Segments()))
	}
}
