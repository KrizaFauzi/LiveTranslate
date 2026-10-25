package transcribe

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const backlogLimit = 100

// finalizeTimeout bounds the wait for the provider to deliver the flushed
// transcript. It follows the close budget of the stop route, so a provider
// that needs a moment to answer still delivers the final segment of the run.
var finalizeTimeout = 10 * time.Second

// failureGrace bounds the wait for the failure that ended the provider. The
// provider reports its failure on another channel than its end, so the session
// waits a moment for it: a failed transcription must never reach the client
// as a clean end.
var failureGrace = 500 * time.Millisecond

// subscriberCapacity holds a whole backlog replay plus live segments, so the
// replay never blocks the publisher and the segment order stays intact.
const subscriberCapacity = backlogLimit

// errProviderFailed is the generic failure a session keeps when the provider fails.
// The provider error text never reaches the client, so no provider detail can leak.
var errProviderFailed = errors.New("transkripsi gagal")

// providerError keeps the provider failure as its cause while reporting a generic message.
type providerError struct{ cause error }

func (e providerError) Error() string { return errProviderFailed.Error() }
func (e providerError) Unwrap() error { return e.cause }

// FinalSink receives canonical finals before subscriber fan-out. Both methods
// must be nonblocking memory operations; storage I/O belongs to the sink worker.
type FinalSink interface {
	Enqueue(Segment)
	Finish()
}

// Session is one live transcription for one run.
type Session struct {
	stream Stream
	// cancel ends the context of the provider stream. The session owns the
	// context, so the provider connection lives exactly as long as the session.
	cancel context.CancelFunc
	sink   FinalSink
	mu     sync.Mutex
	seq    int
	// backlog keeps the latest segments so a late subscriber can catch up.
	backlog []Segment
	// subscribers maps every subscriber channel to the last segment it already has.
	subscribers map[chan Segment]int
	closed      bool
	// err keeps the terminal provider failure, guarded by mu.
	err error
	// providerEnded marks the end that the provider caused: a failure or an
	// end of the provider stream. The close of the owner is not a provider end:
	// the run of an owner close is closed by the stop route.
	providerEnded bool
	// done is closed when the session ends, so every goroutine of the session stops.
	done     chan struct{}
	doneOnce sync.Once
	// ended is closed when the session ended, so the broker can drop it.
	ended     chan struct{}
	endedOnce sync.Once
	// finished is closed when the provider stream of the session ended.
	finished chan struct{}
	finalCh  chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
	// streamOnce closes the provider stream once, whoever ends the session.
	streamOnce sync.Once
	closeErr   error
}

// NewSession starts one live transcription and publishes the provider results to its subscribers.
// The cancel function ends the context of the provider stream, so the provider
// connection is torn down together with the session.
func NewSession(stream Stream, cancel context.CancelFunc, sink FinalSink) *Session {
	session := &Session{
		stream:      stream,
		cancel:      cancel,
		sink:        sink,
		subscribers: map[chan Segment]int{},
		done:        make(chan struct{}),
		ended:       make(chan struct{}),
		finished:    make(chan struct{}),
		finalCh:     make(chan struct{}, 1),
	}
	session.wg.Add(1)
	go session.supervise()
	return session
}

// Write forwards audio bytes to the provider stream.
func (s *Session) Write(payload []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("sesi transkripsi sudah ditutup")
	}
	return s.stream.Write(payload)
}

// Close ends the session: it flushes the pending transcript, stops the provider
// stream and closes every subscriber channel.
func (s *Session) Close() error {
	s.once.Do(func() { s.end() })
	return s.closeErr
}

// Abort cancels provider work when the owner's finalization budget expires.
// It does not wait for an in-progress Close or a slow provider teardown.
func (s *Session) Abort() {
	s.cancelContext()
	s.shutdown()
	s.closeDone()
	s.closeEnded()
}

// end releases the session. The flush waits for the pending transcript of the
// provider, so the final segment of the run reaches the subscribers.
func (s *Session) end() {
	s.flush()
	s.shutdown()
	s.closeDone()
	s.closeEnded()
	s.closeStream()
	s.wg.Wait()
	s.cancelContext()
}

// cancelContext ends the context of the provider stream, so the provider
// connection is torn down with the session.
func (s *Session) cancelContext() {
	if s.cancel != nil {
		s.cancel()
	}
}

// closeDone ends the goroutines of the session.
func (s *Session) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

// closeEnded reports the end of the session to the broker.
func (s *Session) closeEnded() {
	s.endedOnce.Do(func() { close(s.ended) })
}

// flush asks the provider for the pending transcript and waits for the final
// segment it produces. The wait ends as soon as a final segment is published or
// the provider ends, so a provider without a final segment never stalls the close
// longer than the close budget of the stop route.
func (s *Session) flush() {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case <-s.finalCh:
	default:
	}
	_ = s.stream.Finalize()
	select {
	case <-s.finalCh:
	case <-s.finished:
	case <-time.After(finalizeTimeout):
	}
}

// Subscribe registers a subscriber and replays the backlog newer than since.
func (s *Session) Subscribe(since int) (<-chan Segment, func()) {
	subscriber := make(chan Segment, subscriberCapacity)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		close(subscriber)
		return subscriber, func() {}
	}
	for _, segment := range s.backlog {
		if segment.Seq > since {
			subscriber <- segment
		}
	}
	s.subscribers[subscriber] = since
	s.mu.Unlock()
	var once sync.Once
	return subscriber, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, ok := s.subscribers[subscriber]; ok {
				delete(s.subscribers, subscriber)
				close(subscriber)
			}
		})
	}
}

// Err returns the terminal provider failure of the session, or nil when the
// session ended normally.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// SubscriberCount reports how many subscribers the session still holds.
func (s *Session) SubscriberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscribers)
}

// Segments returns a copy of the backlog.
func (s *Session) Segments() []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	segments := make([]Segment, len(s.backlog))
	copy(segments, s.backlog)
	return segments
}

// Ended returns the channel that is closed when the session ended.
func (s *Session) Ended() <-chan struct{} { return s.ended }

// ProviderEnded reports whether the provider ended the session on its own, with
// a failure or with the end of its stream. The close of the owner is not a
// provider end: the run of an owner close is closed by the stop route, so the
// broker leaves that run alone.
func (s *Session) ProviderEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerEnded
}

// publish turns a provider result into a sequenced segment and hands it to the subscribers.
// A result without text is dropped, so the sequence counts only published segments.
func (s *Session) publish(result Result) {
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.seq++
	result.Text = text
	segment := segmentFrom(result, s.seq)
	s.backlog = append(s.backlog, segment)
	if len(s.backlog) > backlogLimit {
		s.backlog = s.backlog[len(s.backlog)-backlogLimit:]
	}
	if result.IsFinal {
		if s.sink != nil {
			s.sink.Enqueue(segment)
		}
		select {
		case s.finalCh <- struct{}{}:
		default:
		}
	}
	for subscriber, since := range s.subscribers {
		if segment.Seq <= since {
			continue
		}
		select {
		case subscriber <- segment:
		default:
		}
	}
}

// shutdown closes the session to new subscribers and closes every subscriber channel.
func (s *Session) shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	subscribers := s.subscribers
	s.subscribers = map[chan Segment]int{}
	s.mu.Unlock()
	for subscriber := range subscribers {
		close(subscriber)
	}
	if s.sink != nil {
		s.sink.Finish()
	}
}

// closeStream stops the provider stream once.
func (s *Session) closeStream() error {
	s.streamOnce.Do(func() { s.closeErr = s.stream.Close() })
	return s.closeErr
}

// supervise owns the terminal state of the session: it publishes the provider
// results, keeps the provider failure, and ends the session exactly once.
//
// The provider reports its failure and its end on two channels, so the session
// reads both here. The failure that ends the provider is known before the
// session ends, so a client never sees a clean end for a failed transcription.
func (s *Session) supervise() {
	defer s.wg.Done()
	defer close(s.finished)
	results := s.stream.Results()
	failures := s.stream.Errors()
	for {
		select {
		case <-s.done:
			return
		case failure, ok := <-failures:
			if !ok {
				// The provider closed its failure channel without a failure.
				failures = nil
				continue
			}
			s.setErr(failure)
			s.drainResults(results)
			s.terminate(true)
			return
		case result, ok := <-results:
			if !ok {
				// The provider ended its stream. The owner may have closed the
				// session in the meantime: then the end belongs to the owner.
				select {
				case <-s.done:
					return
				default:
				}
				// The failure that ended the provider may still be on its way,
				// so the session waits for it before it ends.
				if failure, ok := s.waitFailure(failures); ok {
					s.setErr(failure)
					s.drainResults(results)
				}
				s.terminate(true)
				return
			}
			s.publish(result)
		}
	}
}

// drainResults retains results already delivered when a provider reports its
// failure on the independent error channel.
func (s *Session) drainResults(results <-chan Result) {
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return
			}
			s.publish(result)
		default:
			return
		}
	}
}

// waitFailure waits for the failure that ended the provider. The wait ends as
// soon as the failure arrives, so a provider that ends without a failure does
// not stall the session longer than the grace.
func (s *Session) waitFailure(failures <-chan error) (error, bool) {
	if failures == nil {
		return nil, false
	}
	timer := time.NewTimer(failureGrace)
	defer timer.Stop()
	select {
	case failure, ok := <-failures:
		if !ok {
			return nil, false
		}
		return failure, true
	case <-timer.C:
		return nil, false
	}
}

// terminate ends the session: it closes the subscribers, releases the
// goroutines and drops the session from the broker. The provider stream is
// closed after the session is reported as ended, so a slow teardown does not
// keep a dead session in the broker.
func (s *Session) terminate(providerEnded bool) {
	s.mu.Lock()
	s.providerEnded = providerEnded
	s.mu.Unlock()
	s.shutdown()
	s.closeDone()
	s.closeEnded()
	s.closeStream()
	s.cancelContext()
}

// setErr keeps the terminal provider failure so a client can tell a provider
// failure from a session that ended normally.
func (s *Session) setErr(failure error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	if failure == nil {
		s.err = errProviderFailed
		return
	}
	s.err = providerError{cause: failure}
}
