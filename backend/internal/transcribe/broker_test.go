package transcribe

import (
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeStream struct {
	mu        sync.Mutex
	writes    [][]byte
	results   chan Result
	errors    chan error
	closed    bool
	finalText string
}

func (s *fakeStream) Write(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, payload)
	return nil
}

// Finalize flushes the provider: it publishes the final transcript when the fake
// holds one, but it never ends the stream on its own.
func (s *fakeStream) Finalize() error {
	if s.finalText == "" {
		return nil
	}
	select {
	case s.results <- Result{Text: s.finalText, IsFinal: true}:
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

func (s *fakeStream) Results() <-chan Result { return s.results }
func (s *fakeStream) Errors() <-chan error   { return s.errors }

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
	if stream.writeCount() != 1 {
		t.Fatalf("unexpected writes: %d", stream.writeCount())
	}
}

func TestCloseDeliversTheFinalSegment(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1), finalText: "selamat pagi"}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	start := time.Now()
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
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("close waited %s for the final segment", elapsed)
	}
}

func TestCloseReturnsQuicklyWithoutAFinalSegment(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("close waited %s for a final segment that never came", elapsed)
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
	if !stream.isClosed() {
		t.Fatal("expected the provider stream to be closed")
	}
	unsubscribe()
	for {
		if _, ok := <-segments; !ok {
			break
		}
	}
}

func TestSessionErrKeepsTheProviderFailure(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	if session.Err() != nil {
		t.Fatal("expected no failure before the provider fails")
	}
	stream.errors <- errors.New("provider body: rahasia")
	deadline := time.Now().Add(2 * time.Second)
	for session.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	failure := session.Err()
	if failure == nil {
		t.Fatal("expected the provider failure to be kept")
	}
	if failure.Error() == "" {
		t.Fatal("expected a failure message")
	}
	if strings.Contains(failure.Error(), "rahasia") {
		t.Fatalf("the provider detail leaked: %q", failure.Error())
	}
}

func TestSessionErrIsNilAfterANormalEnd(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 1), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if session.Err() != nil {
		t.Fatalf("expected no failure after a normal end: %v", session.Err())
	}
}

// slowProvider blocks in Open until its context ends, like a provider that
// cannot be reached.
type slowProvider struct {
	started chan context.Context
}

func newSlowProvider() *slowProvider {
	return &slowProvider{started: make(chan context.Context, 1)}
}

func (p *slowProvider) Open(ctx context.Context, _ int) (Stream, error) {
	p.started <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBrokerOpenBoundsTheHandshake(t *testing.T) {
	original := handshakeTimeout
	handshakeTimeout = 100 * time.Millisecond
	defer func() { handshakeTimeout = original }()
	provider := newSlowProvider()
	broker := NewBroker(config.Config{}, provider)
	start := time.Now()
	opened := make(chan error, 1)
	go func() {
		_, err := broker.Open(context.Background(), "run-1", 48000)
		opened <- err
	}()
	var err error
	select {
	case err = <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("the handshake was not bounded")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the handshake to fail")
	}
	if !errors.Is(err, ErrProviderConnect) {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 2*handshakeTimeout {
		t.Fatalf("the handshake was not bounded: %s", elapsed)
	}
	// The session context ends with the failed handshake, so the blocked
	// provider call does not leak.
	sessionCtx := <-provider.started
	if err := sessionCtx.Err(); err == nil {
		t.Fatal("the session context outlived the failed handshake")
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
