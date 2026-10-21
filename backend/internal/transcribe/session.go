package transcribe

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const backlogLimit = 100

// finalizeTimeout bounds the wait for the provider to deliver the flushed transcript.
const finalizeTimeout = 500 * time.Millisecond

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

// Session is one live transcription for one run.
type Session struct {
	stream Stream
	mu     sync.Mutex
	seq    int
	// backlog keeps the latest segments so a late subscriber can catch up.
	backlog []Segment
	// subscribers maps every subscriber channel to the last segment it already has.
	subscribers map[chan Segment]int
	closed      bool
	errCh       chan error
	// err keeps the terminal provider failure, guarded by mu.
	err        error
	done       chan struct{}
	finished   chan struct{}
	finalCh    chan struct{}
	wg         sync.WaitGroup
	once       sync.Once
	streamOnce sync.Once
	closeErr   error
}

// NewSession starts one live transcription and publishes the provider results to its subscribers.
func NewSession(stream Stream) *Session {
	session := &Session{
		stream:      stream,
		subscribers: map[chan Segment]int{},
		errCh:       make(chan error, 1),
		done:        make(chan struct{}),
		finished:    make(chan struct{}),
		finalCh:     make(chan struct{}, 1),
	}
	session.wg.Add(3)
	go session.forwardResults()
	go session.forwardErrors()
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
	s.once.Do(func() {
		s.flush()
		s.shutdown()
		close(s.done)
		s.closeStream()
		s.wg.Wait()
		close(s.errCh)
	})
	return s.closeErr
}

// flush asks the provider for the pending transcript and waits for the final
// segment it produces. The wait ends as soon as a final segment is published or
// the provider ends, so a provider without a final segment never stalls the close.
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

// Segments returns a copy of the backlog.
func (s *Session) Segments() []Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	segments := make([]Segment, len(s.backlog))
	copy(segments, s.backlog)
	return segments
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
}

// closeStream stops the provider stream once.
func (s *Session) closeStream() error {
	s.streamOnce.Do(func() { s.closeErr = s.stream.Close() })
	return s.closeErr
}

// forwardResults publishes the provider results until the provider ends.
func (s *Session) forwardResults() {
	defer s.wg.Done()
	defer close(s.finished)
	for {
		select {
		case <-s.done:
			return
		case result, ok := <-s.stream.Results():
			if !ok {
				s.shutdown()
				s.closeStream()
				return
			}
			s.publish(result)
		}
	}
}

// forwardErrors reports the provider failures on the session error channel.
func (s *Session) forwardErrors() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		case err, ok := <-s.stream.Errors():
			if !ok {
				return
			}
			select {
			case s.errCh <- err:
			default:
			}
		}
	}
}

// supervise closes the session when the provider reports a failure.
func (s *Session) supervise() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		case failure, ok := <-s.errCh:
			if !ok {
				return
			}
			s.setErr(failure)
			s.shutdown()
			s.closeStream()
			return
		}
	}
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
