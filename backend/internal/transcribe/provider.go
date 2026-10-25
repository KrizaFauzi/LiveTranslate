package transcribe

import (
	"context"
	"errors"
)

// ErrProviderConfig marks a provider whose configuration is incomplete, so the
// caller can tell a missing configuration from a failed connection.
var ErrProviderConfig = errors.New("konfigurasi Deepgram belum lengkap")

// ErrProviderConnect marks a provider connection that could not be established.
var ErrProviderConnect = errors.New("koneksi Deepgram gagal")

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

// Configurable is a provider that can report whether its configuration is
// complete. A provider that cannot report it is assumed to be ready.
type Configurable interface {
	Configured() bool
}
