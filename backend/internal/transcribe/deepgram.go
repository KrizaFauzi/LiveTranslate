package transcribe

import (
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"os"
	"sync"
	"sync/atomic"
	"time"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	clientinterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	clientws "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen/v1/websocket"
)

const writeTimeout = 5 * time.Second

// streamTimeouts bounds the provider calls that can block on a stuck connection.
type streamTimeouts struct {
	write    time.Duration
	finalize time.Duration
	teardown time.Duration
}

var defaultTimeouts = streamTimeouts{write: writeTimeout, finalize: writeTimeout, teardown: writeTimeout}

// deepgramClient is the part of the Deepgram SDK client the stream uses. The SDK
// client holds its connection mutex while writing to the socket, so one stuck
// write blocks every later call; the interface keeps that behaviour testable
// without a network.
type deepgramClient interface {
	Write(payload []byte) (int, error)
	Finalize() error
	Finish()
	Stop()
}

// deepgramProvider opens Deepgram live transcription streams.
type deepgramProvider struct {
	cfg config.Config
}

func newDeepgramProvider(cfg config.Config) Provider {
	return &deepgramProvider{cfg: cfg}
}

// Open connects to the Deepgram live API and starts forwarding provider messages.
func (p *deepgramProvider) Open(ctx context.Context, sampleRate int) (Stream, error) {
	chans := newDeepgramChans()
	options := liveOptions(p.cfg.DeepgramModel, deepgramLanguage(), sampleRate)
	client, err := clientws.NewUsingChan(ctx, p.cfg.DeepgramKey, &clientinterfaces.ClientOptions{}, options, chans)
	if err != nil || client == nil {
		return nil, errors.New("konfigurasi Deepgram tidak valid")
	}
	if !client.Connect() {
		client.Stop()
		return nil, errors.New("koneksi Deepgram gagal")
	}
	return newDeepgramStream(client, chans), nil
}

// deepgramLanguage reads the transcription language until the config carries it.
func deepgramLanguage() string {
	language := os.Getenv("DEEPGRAM_LANGUAGE")
	if language == "" {
		return "multi"
	}
	return language
}

// deepgramStream is one live Deepgram connection.
type deepgramStream struct {
	client   deepgramClient
	chans    *deepgramChans
	results  chan Result
	errors   chan error
	done     chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
	timeouts streamTimeouts
	poisoned atomic.Bool
}

func newDeepgramStream(client deepgramClient, chans *deepgramChans) *deepgramStream {
	stream := &deepgramStream{
		client:   client,
		chans:    chans,
		results:  make(chan Result, 64),
		errors:   make(chan error, 8),
		done:     make(chan struct{}),
		timeouts: defaultTimeouts,
	}
	stream.wg.Add(2)
	go stream.forwardResults()
	go stream.forwardErrors()
	return stream
}

// Write sends audio bytes to Deepgram without exposing their content.
func (s *deepgramStream) Write(payload []byte) error {
	if s.poisoned.Load() {
		return errors.New("Deepgram: penulisan gagal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeouts.write)
	defer cancel()
	written := make(chan error, 1)
	go func() {
		_, err := s.client.Write(payload)
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			return errors.New("Deepgram: penulisan gagal")
		}
		return nil
	case <-ctx.Done():
		s.poisoned.Store(true)
		return errors.New("Deepgram: penulisan gagal")
	}
}

// Finalize asks Deepgram to flush the pending transcript.
func (s *deepgramStream) Finalize() error {
	if s.poisoned.Load() {
		return errors.New("Deepgram: finalisasi dilewati karena koneksi macet")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeouts.finalize)
	defer cancel()
	finalized := make(chan error, 1)
	go func() {
		finalized <- s.client.Finalize()
	}()
	select {
	case err := <-finalized:
		if err != nil {
			return errors.New("Deepgram: finalisasi gagal")
		}
		return nil
	case <-ctx.Done():
		s.poisoned.Store(true)
		return errors.New("Deepgram: finalisasi gagal")
	}
}

// Close ends the connection and closes the result and error channels. It returns
// even when the provider connection is stuck.
func (s *deepgramStream) Close() error {
	s.once.Do(func() {
		s.stopClient()
		close(s.done)
		s.wg.Wait()
	})
	return nil
}

// stopClient tears the socket down without waiting for a stuck connection. The
// SDK keeps its connection mutex while writing, so Stop can wait forever once a
// write is stuck; the call then continues in the background instead of holding
// the caller.
func (s *deepgramStream) stopClient() {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeouts.teardown)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		s.client.Finish() // no-op in SDK v3.8.0; Stop does the real teardown
		s.client.Stop()
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
	}
}

// Results returns the channel of provider results.
func (s *deepgramStream) Results() <-chan Result {
	return s.results
}

// Errors returns the channel of provider failures.
func (s *deepgramStream) Errors() <-chan error {
	return s.errors
}

func (s *deepgramStream) forwardResults() {
	defer s.wg.Done()
	defer close(s.results)
	messages := *s.chans.GetMessage()[0]
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				return
			}
			select {
			case s.results <- toResult(message):
			case <-s.done:
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *deepgramStream) forwardErrors() {
	defer s.wg.Done()
	defer close(s.errors)
	failures := *s.chans.GetError()[0]
	for {
		select {
		case _, ok := <-failures:
			if !ok {
				return
			}
			select {
			case s.errors <- errors.New("transkripsi Deepgram gagal"):
			case <-s.done:
				return
			}
		case <-s.done:
			return
		}
	}
}

func liveOptions(model, language string, sampleRate int) *clientinterfaces.LiveTranscriptionOptions {
	return &clientinterfaces.LiveTranscriptionOptions{
		Model:          model,
		Language:       language,
		Encoding:       "linear16",
		SampleRate:     sampleRate,
		InterimResults: true,
		Punctuate:      true,
		SmartFormat:    true,
	}
}

func toResult(message *msginterfaces.MessageResponse) Result {
	result := Result{IsFinal: message.IsFinal, Start: message.Start, Duration: message.Duration}
	if len(message.Channel.Alternatives) > 0 {
		result.Text = message.Channel.Alternatives[0].Transcript
		if len(message.Channel.Alternatives[0].Languages) > 0 {
			result.Language = message.Channel.Alternatives[0].Languages[0]
		}
	}
	return result
}

// deepgramChans receives provider messages. The SDK default handler prints transcripts
// to stdout and drains the message channel, so this handler keeps them for the stream.
type deepgramChans struct {
	messages chan *msginterfaces.MessageResponse
	failures chan *msginterfaces.ErrorResponse
}

func newDeepgramChans() *deepgramChans {
	return &deepgramChans{
		messages: make(chan *msginterfaces.MessageResponse, 64),
		failures: make(chan *msginterfaces.ErrorResponse, 8),
	}
}

func (c *deepgramChans) GetOpen() []*chan *msginterfaces.OpenResponse {
	return nil
}

func (c *deepgramChans) GetMessage() []*chan *msginterfaces.MessageResponse {
	return []*chan *msginterfaces.MessageResponse{&c.messages}
}

func (c *deepgramChans) GetMetadata() []*chan *msginterfaces.MetadataResponse {
	return nil
}

func (c *deepgramChans) GetSpeechStarted() []*chan *msginterfaces.SpeechStartedResponse {
	return nil
}

func (c *deepgramChans) GetUtteranceEnd() []*chan *msginterfaces.UtteranceEndResponse {
	return nil
}

func (c *deepgramChans) GetClose() []*chan *msginterfaces.CloseResponse {
	return nil
}

func (c *deepgramChans) GetError() []*chan *msginterfaces.ErrorResponse {
	return []*chan *msginterfaces.ErrorResponse{&c.failures}
}

func (c *deepgramChans) GetUnhandled() []*chan *[]byte {
	return nil
}
