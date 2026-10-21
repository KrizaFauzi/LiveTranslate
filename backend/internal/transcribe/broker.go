package transcribe

import (
	"context"
	"livetranslate/backend/internal/config"
	"sync"
	"time"
)

// handshakeTimeout bounds the handshake of the provider. The Deepgram SDK
// retries a connection for much longer than a request may wait, so the
// handshake has its own limit while the connection itself lives as long as the
// session.
var handshakeTimeout = config.ConnectionTimeout

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
	// The provider connection must outlive the request that opens it, so the
	// session owns its own context: it keeps the values of the caller but
	// neither its deadline nor its cancellation. The session ends the context
	// when it closes, so the connection dies with the session.
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stream, err := openProvider(sessionCtx, b.provider, sampleRate)
	if err != nil {
		cancel()
		return nil, err
	}
	session := NewSession(stream, cancel)
	b.mu.Lock()
	b.sessions[runID] = session
	b.mu.Unlock()
	return session, nil
}

// openProvider opens the provider stream within the handshake budget. The
// provider runs in the background, so a provider that keeps retrying cannot
// hold the request that opens the session. The session context is ended by the
// caller when the budget expires, so the provider call and its connection die
// with it.
func openProvider(ctx context.Context, provider Provider, sampleRate int) (Stream, error) {
	type outcome struct {
		stream Stream
		err    error
	}
	opened := make(chan outcome, 1)
	go func() {
		stream, err := provider.Open(ctx, sampleRate)
		opened <- outcome{stream, err}
	}()
	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()
	select {
	case result := <-opened:
		return result.stream, result.err
	case <-timer.C:
		return nil, ErrProviderConnect
	}
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
