package transcribe

import (
	"context"
	"livetranslate/backend/internal/config"
	"sync"
)

// Broker keeps one live transcription session per capture run.
type Broker struct {
	mu       sync.Mutex
	sessions map[string]*Session
	provider Provider
}

// NewBroker creates a broker. A nil provider falls back to the Deepgram provider.
func NewBroker(cfg config.Config, provider Provider) *Broker {
	if provider == nil {
		provider = newDeepgramProvider(cfg)
	}
	return &Broker{sessions: map[string]*Session{}, provider: provider}
}

// Open starts the live transcription of one run.
func (b *Broker) Open(ctx context.Context, runID string, sampleRate int) (*Session, error) {
	stream, err := b.provider.Open(ctx, sampleRate)
	if err != nil {
		return nil, err
	}
	session := NewSession(stream)
	b.mu.Lock()
	b.sessions[runID] = session
	b.mu.Unlock()
	return session, nil
}

// Configured reports whether the provider has the configuration it needs to open
// a stream. A provider that cannot report its configuration is assumed to be ready.
func (b *Broker) Configured() bool {
	provider, ok := b.provider.(Configurable)
	if !ok {
		return true
	}
	return provider.Configured()
}

// Session returns the live session of one run.
func (b *Broker) Session(runID string) (*Session, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session, ok := b.sessions[runID]
	return session, ok
}

// Remove drops the session of one run.
func (b *Broker) Remove(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sessions, runID)
}
