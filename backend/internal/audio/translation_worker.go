package audio

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"livetranslate/backend/internal/sumopod"
	"livetranslate/backend/internal/transcribe"
)

const translationQueueCount = 128
const translationBudget = 15 * time.Second

type translationWorker struct {
	repo     repository
	run      Run
	provider *sumopod.Client
	queue    chan transcribe.Segment
	mu       sync.Mutex
	token    string
	closed   bool
	overflow atomic.Bool
}

func newTranslationWorker(repo repository, run Run, token string, provider *sumopod.Client) *translationWorker {
	if provider == nil || !provider.Available() {
		return nil
	}
	w := &translationWorker{repo: repo, run: run, provider: provider, token: token, queue: make(chan transcribe.Segment, translationQueueCount)}
	go w.work()
	return w
}

func (w *translationWorker) refresh(user, token string) {
	if w == nil || user != w.run.UserID {
		return
	}
	w.mu.Lock()
	w.token = token
	w.mu.Unlock()
}
func (w *translationWorker) credential() string { w.mu.Lock(); defer w.mu.Unlock(); return w.token }
func (w *translationWorker) enqueue(segment transcribe.Segment) {
	if w == nil || segment.Type != "final" || strings.TrimSpace(segment.Text) == "" {
		return
	}
	select {
	case w.queue <- segment:
	default:
		w.overflow.Store(true)
	}
}
func (w *translationWorker) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		close(w.queue)
		w.closed = true
	}
	w.mu.Unlock()
}
func (w *translationWorker) work() {
	for segment := range w.queue {
		ctx, cancel := context.WithTimeout(context.Background(), translationBudget)
		text, err := w.provider.TranslateIndonesian(ctx, segment.Language, segment.Text)
		status, code := "success", ""
		if err != nil {
			text, status, code = "", "failed", err.Error()
		}
		if stored, _ := w.repo.putTranslation(ctx, w.run, segment.Seq, text, status, code, w.credential()); !stored {
			w.overflow.Store(true)
		}
		cancel()
	}
	if w.overflow.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = w.repo.failMissingTranslations(ctx, w.run, w.credential())
		cancel()
	}
}
