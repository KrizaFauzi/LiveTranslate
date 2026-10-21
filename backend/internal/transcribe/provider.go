package transcribe

import "context"

// Stream is one live provider connection.
type Stream interface {
	Write(payload []byte) error
	Finalize() error
	Close() error
	Results() <-chan Result
	Errors() <-chan error
}

// Provider opens provider streams. Tests inject a fake implementation.
type Provider interface {
	Open(ctx context.Context, sampleRate int) (Stream, error)
}
