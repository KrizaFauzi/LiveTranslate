package audio

import (
	"context"
	"sync"
	"time"

	"livetranslate/backend/internal/transcribe"
)

const transcriptQueueCount = 128
const transcriptQueueBytes = 2 << 20
const transcriptRequestBudget = 10 * time.Second
const transcriptDrainBudget = 30 * time.Second

// transcriptWriter is a nonblocking final sink with one ordered I/O worker.
// Its context is independent of HTTP requests and lives through final drain.
type transcriptWriter struct {
	repo        repository
	run         Run
	mu          sync.Mutex
	token       string
	ticket      uint64
	queue       chan transcribe.Segment
	wake        chan struct{}
	queuedBytes int
	finished    bool
	status      string
	deadline    time.Time
	timer       *time.Timer
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	now         func() time.Time
	after       func(time.Duration, func()) *time.Timer
	onStored    func(transcribe.Segment)
}

func newTranscriptWriter(repo repository, run Run, token string, ticket uint64) *transcriptWriter {
	return newTranscriptWriterWithCallback(repo, run, token, ticket, nil)
}

func newTranscriptWriterWithCallback(repo repository, run Run, token string, ticket uint64, onStored func(transcribe.Segment)) *transcriptWriter {
	return startTranscriptWriter(repo, run, token, ticket, time.Now, time.AfterFunc, onStored)
}

func startTranscriptWriter(repo repository, run Run, token string, ticket uint64, now func() time.Time, after func(time.Duration, func()) *time.Timer, callbacks ...func(transcribe.Segment)) *transcriptWriter {
	ctx, cancel := context.WithCancel(context.Background())
	var onStored func(transcribe.Segment)
	if len(callbacks) != 0 {
		onStored = callbacks[0]
	}
	w := &transcriptWriter{repo: repo, run: run, token: token, ticket: ticket, queue: make(chan transcribe.Segment, transcriptQueueCount), wake: make(chan struct{}, 1), status: "saving", ctx: ctx, cancel: cancel, done: make(chan struct{}), now: now, after: after, onStored: onStored}
	go w.work()
	return w
}

func (w *transcriptWriter) state() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *transcriptWriter) refresh(user, token string, ticket uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if user == w.run.UserID && ticket > w.ticket {
		w.token = token
		w.ticket = ticket
	}
}

func (w *transcriptWriter) credential() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.token
}

func (w *transcriptWriter) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *transcriptWriter) Enqueue(segment transcribe.Segment) {
	if segment.Type != "final" || segment.Text == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished || w.status != "saving" {
		return
	}
	if len(segment.Text) > transcriptQueueBytes-w.queuedBytes {
		w.failLocked()
		return
	}
	select {
	case w.queue <- segment:
		w.queuedBytes += len(segment.Text)
	default:
		w.failLocked()
	}
}

func (w *transcriptWriter) failLocked() {
	if w.status == "saving" {
		w.status = "failed"
	}
	w.cancel()
	w.signal()
}

// beginDrain starts once, at stop (before provider finalization), or at provider
// termination via Finish. All I/O uses the remaining shared 30-second budget.
func (w *transcriptWriter) beginDrain() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.beginDrainLocked()
}

func (w *transcriptWriter) beginDrainLocked() {
	if !w.deadline.IsZero() || w.status != "saving" {
		return
	}
	w.deadline = w.now().Add(transcriptDrainBudget)
	w.timer = w.after(transcriptDrainBudget, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.status == "saving" {
			w.failLocked()
		}
	})
}

func (w *transcriptWriter) Finish() {
	w.mu.Lock()
	w.finished = true
	w.beginDrainLocked()
	w.mu.Unlock()
	w.signal()
}

// terminate is invoked only after the database confirms delete/expiry/disable.
func (w *transcriptWriter) terminate(state string) {
	w.mu.Lock()
	if (w.status == "deleted" || w.status == "expired" || w.status == "disabled") && state == "failed" {
		w.mu.Unlock()
		return
	}
	w.status = state
	w.finished = true
	w.mu.Unlock()
	w.cancel()
	w.signal()
}

func (w *transcriptWriter) work() {
	defer close(w.done)
	defer w.cancel()
	defer func() {
		w.mu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		for len(w.queue) > 0 {
			<-w.queue
		}
		w.queuedBytes = 0
		w.mu.Unlock()
	}()
	for {
		w.mu.Lock()
		status, finished, empty := w.status, w.finished, len(w.queue) == 0
		w.mu.Unlock()
		if status != "saving" {
			w.recordFailure()
			return
		}
		if finished && empty {
			w.complete()
			return
		}
		select {
		case <-w.ctx.Done():
			w.mu.Lock()
			if w.status == "saving" {
				w.status = "failed"
			}
			w.mu.Unlock()
		case <-w.wake:
		case segment := <-w.queue:
			w.mu.Lock()
			w.queuedBytes -= len(segment.Text)
			active := w.status == "saving"
			w.mu.Unlock()
			if !active {
				continue
			}
			ctx, cancel := context.WithTimeout(w.ctx, transcriptRequestBudget)
			accepted, err := w.repo.appendFinal(ctx, w.run, segment, w.credential())
			cancel()
			if err != nil {
				w.mu.Lock()
				w.failLocked()
				w.mu.Unlock()
				continue
			}
			if !accepted {
				w.unavailable()
				continue
			}
			if w.onStored != nil {
				w.onStored(segment)
			}
		}
	}
}

func (w *transcriptWriter) unavailable() {
	ctx, cancel := context.WithTimeout(w.ctx, transcriptRequestBudget)
	defer cancel()
	run, found, err := w.repo.get(ctx, w.run.ID, w.run.UserID, w.credential())
	state := "failed"
	if err == nil && found {
		if run.TranscriptState == "deleted" {
			state = "deleted"
		} else if !run.SaveTranscript {
			state = "disabled"
		} else if expiry, parseErr := time.Parse(time.RFC3339Nano, string(run.TranscriptExpiresAt)); parseErr == nil && !expiry.After(w.now()) {
			state = "expired"
		}
	}
	w.terminate(state)
}

func (w *transcriptWriter) complete() {
	ctx, cancel := context.WithTimeout(w.ctx, transcriptRequestBudget)
	accepted, err := w.repo.setTranscriptState(ctx, w.run, w.credential(), "saved")
	cancel()
	if err != nil {
		w.mu.Lock()
		w.failLocked()
		w.mu.Unlock()
		w.recordFailure()
		return
	}
	if !accepted {
		w.unavailable()
		w.recordFailure()
		return
	}
	w.mu.Lock()
	// A concurrent overflow/deletion/deadline must never be overwritten.
	if w.status == "saving" {
		w.status = "saved"
	}
	failed := w.status == "failed"
	w.mu.Unlock()
	if failed {
		w.recordFailure()
	}
}

func (w *transcriptWriter) recordFailure() {
	w.mu.Lock()
	state, deadline := w.status, w.deadline
	w.mu.Unlock()
	if state != "failed" {
		return
	}
	budget := transcriptRequestBudget
	if !deadline.IsZero() {
		remaining := deadline.Sub(w.now())
		if remaining < budget {
			budget = remaining
		}
	}
	if budget <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	// Failure to acknowledge this state remains saving in the DB; read derives
	// incomplete after restart. There is deliberately no retry or outbox.
	_, _ = w.repo.setTranscriptState(ctx, w.run, w.credential(), "failed")
}
