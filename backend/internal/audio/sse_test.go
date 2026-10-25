package audio

import (
	"bufio"
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// eventWriter records the response of the stream. The first write ends the
// provider stream, so the session closes and the handler finishes on its own,
// without the test having to wait for a timer.
type eventWriter struct {
	mu     sync.Mutex
	header http.Header
	code   int
	body   strings.Builder
	once   sync.Once
	end    func()
}

func (w *eventWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *eventWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.code = code
}

func (w *eventWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	w.body.Write(payload)
	w.mu.Unlock()
	w.once.Do(func() {
		if w.end != nil {
			w.end()
		}
	})
	return len(payload), nil
}

// status returns the status of the response.
func (w *eventWriter) status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code
}

// content returns the events of the response.
func (w *eventWriter) content() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// streamRequest builds the request of the transcript route.
func streamRequest(path, token string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// endSession ends the provider stream of a session once, so the session closes
// and the handler returns.
func endSession(stream *fakeStream) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(stream.results) })
	}
}

func TestTranscriptStreamOwnershipAndEvents(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	if w := call(t, h, "GET", "/api/audio/runs/"+unknownRun+"/transcript", "", "owner", ""); w.Code != 404 {
		t.Fatalf("unknown run: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "other", ""); w.Code != 404 {
		t.Fatalf("foreign run: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+orphanRun+"/transcript", "", "owner", ""); w.Code != 503 {
		t.Fatalf("run without live session: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated stream: %d %s", w.Code, w.Body.String())
	}
}

// TestTranscriptStreamEndsForStoppedRun covers a run that is already stopped:
// the stream answers with the end event instead of a missing transcript.
func TestTranscriptStreamEndsForStoppedRun(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	w := call(t, h, "GET", "/api/audio/runs/"+stoppedRun+"/transcript", "", "owner", "")
	if w.Code != 200 {
		t.Fatalf("stopped run: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: end") {
		t.Fatalf("the stopped run did not end the stream: %s", w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type: %q", contentType)
	}
}

// TestTranscriptStreamPublishesSegments covers the live stream: every segment
// of the session reaches the client as an event.
func TestTranscriptStreamPublishesSegments(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	if _, err := broker.Open(context.Background(), runID, 48000, nil); err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	stream.results <- transcribe.Result{Text: "selamat pagi", IsFinal: true, Start: 1.5, Duration: 0.4, Language: "id"}
	stop := endSession(stream)
	writer := &eventWriter{end: stop}
	// The watchdog ends the session, so a stream that never writes fails the
	// test instead of waiting for the end of the test binary.
	go func() {
		time.Sleep(2 * time.Second)
		stop()
	}()
	h.ServeHTTP(writer, streamRequest("/api/audio/runs/"+runID+"/transcript", "owner"))
	body := writer.content()
	if writer.status() != 200 {
		t.Fatalf("stream: %d %s", writer.status(), body)
	}
	if !strings.Contains(body, "event: segment") || !strings.Contains(body, "selamat pagi") {
		t.Fatalf("the segment did not reach the stream: %s", body)
	}
	if !strings.Contains(body, "event: end") {
		t.Fatalf("the stream did not end: %s", body)
	}
}

// TestTranscriptStreamSkipsTheSegmentsBeforeSince covers the resume of a
// client that already holds the earlier segments.
func TestTranscriptStreamSkipsTheSegmentsBeforeSince(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	if _, err := broker.Open(context.Background(), runID, 48000, nil); err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	stream.results <- transcribe.Result{Text: "segmen satu", IsFinal: false}
	stream.results <- transcribe.Result{Text: "segmen dua", IsFinal: true}
	stop := endSession(stream)
	writer := &eventWriter{end: stop}
	// The watchdog ends the session, so a stream that never writes fails the
	// test instead of waiting for the end of the test binary.
	go func() {
		time.Sleep(2 * time.Second)
		stop()
	}()
	h.ServeHTTP(writer, streamRequest("/api/audio/runs/"+runID+"/transcript?since=1", "owner"))
	body := writer.content()
	if strings.Contains(body, "segmen satu") {
		t.Fatalf("the stream repeated a segment the client already has: %s", body)
	}
	if !strings.Contains(body, "segmen dua") {
		t.Fatalf("the stream missed a segment: %s", body)
	}
}

// TestTranscriptStreamReportsProviderFailure covers a provider that fails: the
// stream reports the failure without leaking the detail of the provider.
func TestTranscriptStreamReportsProviderFailure(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	session, err := broker.Open(context.Background(), runID, 48000, nil)
	if err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	writer := &eventWriter{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(writer, streamRequest("/api/audio/runs/"+runID+"/transcript", "owner"))
	}()
	// The stream is open when the provider fails, like the client of the extension.
	waitForSubscribers(t, session, 1)
	stream.errors <- errors.New("rahasia provider")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end")
	}
	body := writer.content()
	if writer.status() != 200 {
		t.Fatalf("stream: %d %s", writer.status(), body)
	}
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "transcript_error") {
		t.Fatalf("the failure was not reported: %s", body)
	}
	if strings.Contains(body, "rahasia provider") {
		t.Fatalf("the detail of the provider leaked: %s", body)
	}
}

// TestTranscriptStreamReportsTheFailureThatEndedTheProvider covers a provider
// that fails and ends its stream at the same time: the stream reports the
// failure instead of a clean end, because the transcription really failed.
func TestTranscriptStreamReportsTheFailureThatEndedTheProvider(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	session, err := broker.Open(context.Background(), runID, 48000, nil)
	if err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	writer := &eventWriter{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(writer, streamRequest("/api/audio/runs/"+runID+"/transcript", "owner"))
	}()
	waitForSubscribers(t, session, 1)
	// The provider ends its stream and reports the failure right after it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		stream.errors <- errors.New("rahasia provider")
	}()
	close(stream.results)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end")
	}
	body := writer.content()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("the failure was reported as a clean end: %s", body)
	}
	if strings.Contains(body, "event: end") {
		t.Fatalf("the failure was reported as a clean end: %s", body)
	}
	if strings.Contains(body, "rahasia provider") {
		t.Fatalf("the detail of the provider leaked: %s", body)
	}
}

// TestTranscriptStreamSurvivesTheServerWriteTimeout covers the write deadline
// of the server: the stream has to outlive it, because the server writes the
// response within 2 * config.ConnectionTimeout.
func TestTranscriptStreamSurvivesTheServerWriteTimeout(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	if _, err := broker.Open(context.Background(), runID, 48000, nil); err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	server := httptest.NewUnstartedServer(h)
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	defer server.Close()

	request, err := http.NewRequest("GET", server.URL+"/api/audio/runs/"+runID+"/transcript", nil)
	if err != nil {
		t.Fatalf("the request was not built: %v", err)
	}
	request.Header.Set("Authorization", "Bearer owner")
	stop := endSession(stream)
	// The segment arrives after the write deadline of the server, so the
	// stream only survives when the handler clears the deadline.
	go func() {
		time.Sleep(400 * time.Millisecond)
		stream.results <- transcribe.Result{Text: "setelah tenggat", IsFinal: true}
	}()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("the stream did not open: %v", err)
	}
	defer response.Body.Close()
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	events := ""
	wait := time.After(5 * time.Second)
	for !strings.Contains(events, "setelah tenggat") {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("the stream died with the write deadline of the server: %q", events)
			}
			events += line + "\n"
		case <-wait:
			t.Fatalf("the segment did not arrive: %q", events)
		}
	}
	// The session ends, so the handler returns and the server closes the
	// connection.
	stop()
}

// TestTranscriptStreamBoundsTheOwnershipRead covers the phase before the
// stream starts: a slow ownership read must not hold the route without a
// budget.
func TestTranscriptStreamBoundsTheOwnershipRead(t *testing.T) {
	original := transcriptBudget
	transcriptBudget = 200 * time.Millisecond
	defer func() { transcriptBudget = original }()
	stream := newFakeStream()
	h, broker, db := newHandlerWithBroker(t, workingProvider{stream: stream})
	if _, err := broker.Open(context.Background(), runID, 48000, nil); err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	db.slowOwnership(2 * time.Second)
	// The watchdog ends the session, so a route that is not bounded ends the
	// stream instead of blocking the test.
	stop := endSession(stream)
	go func() {
		time.Sleep(3 * time.Second)
		stop()
	}()
	start := time.Now()
	w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "owner", "")
	elapsed := time.Since(start)
	if w.Code != 504 {
		t.Fatalf("the transcript route with a slow ownership read: %d %s", w.Code, w.Body.String())
	}
	if elapsed >= time.Second {
		t.Fatalf("the ownership read was not bounded: %s", elapsed)
	}
}

// TestTranscriptStreamEndsWhenTheClientDisconnects covers the client that
// leaves: the handler returns and the subscription of the session is released.
func TestTranscriptStreamEndsWhenTheClientDisconnects(t *testing.T) {
	stream := newFakeStream()
	h, broker, _ := newHandlerWithBroker(t, workingProvider{stream: stream})
	if _, err := broker.Open(context.Background(), runID, 48000, nil); err != nil {
		t.Fatalf("the session did not open: %v", err)
	}
	session, live := broker.Session(runID)
	if !live {
		t.Fatal("the session is not live")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/api/audio/runs/"+runID+"/transcript", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer owner")
	writer := &eventWriter{}
	returned := make(chan struct{})
	go func() {
		h.ServeHTTP(writer, request)
		close(returned)
	}()
	// The subscription has to exist before the client leaves, so the test proves
	// that a real subscription is created and released again.
	waitForSubscribers(t, session, 1)
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler did not return after the client disconnected")
	}
	if count := session.SubscriberCount(); count != 0 {
		t.Fatalf("the subscription was not released: %d subscribers", count)
	}
	if writer.status() != 200 {
		t.Fatalf("the stream did not start: %d", writer.status())
	}
}

// TestTranscriptBudgetStaysBelowTheServerWriteTimeout keeps the phase before
// the stream inside the window the server has to write the response.
func TestTranscriptBudgetStaysBelowTheServerWriteTimeout(t *testing.T) {
	if transcriptBudget >= 2*config.ConnectionTimeout {
		t.Fatalf("the transcript route can outlive the write timeout of the server: %s", transcriptBudget)
	}
}

// waitForSubscribers waits until the session holds the wanted number of
// subscribers.
func waitForSubscribers(t *testing.T, session *transcribe.Session, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for session.SubscriberCount() != want {
		if time.Now().After(deadline) {
			t.Fatalf("the session holds %d subscribers, want %d", session.SubscriberCount(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
