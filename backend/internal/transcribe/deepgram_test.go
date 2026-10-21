package transcribe

import (
	"sync"
	"testing"
	"time"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

// stuckClient is a provider client that blocks in the calls the SDK blocks in
// when its connection mutex is held by a stuck socket write.
type stuckClient struct {
	mu            sync.Mutex
	calls         []string
	writeBlock    chan struct{}
	finalizeBlock chan struct{}
	stopBlock     chan struct{}
}

func (c *stuckClient) record(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, name)
}

func (c *stuckClient) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, call := range c.calls {
		if call == name {
			total++
		}
	}
	return total
}

func (c *stuckClient) Write(payload []byte) (int, error) {
	c.record("write")
	if c.writeBlock != nil {
		<-c.writeBlock
	}
	return len(payload), nil
}

func (c *stuckClient) Finalize() error {
	c.record("finalize")
	if c.finalizeBlock != nil {
		<-c.finalizeBlock
	}
	return nil
}

func (c *stuckClient) Finish() {
	c.record("finish")
}

func (c *stuckClient) Stop() {
	c.record("stop")
	if c.stopBlock != nil {
		<-c.stopBlock
	}
}

// fastTimeouts keeps the stuck provider tests short.
var fastTimeouts = streamTimeouts{write: 50 * time.Millisecond, finalize: 50 * time.Millisecond, teardown: 50 * time.Millisecond}

func newStuckStream(client deepgramClient) *deepgramStream {
	stream := newDeepgramStream(client, newDeepgramChans())
	stream.timeouts = fastTimeouts
	return stream
}

func TestWriteTimeoutStopsFurtherProviderCalls(t *testing.T) {
	client := &stuckClient{writeBlock: make(chan struct{})}
	defer close(client.writeBlock)
	stream := newStuckStream(client)
	defer stream.Close()

	if err := stream.Write([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected a write error while the provider connection is stuck")
	}
	if err := stream.Write([]byte{4, 5, 6}); err == nil {
		t.Fatal("expected a write error after a write timeout")
	}
	if err := stream.Finalize(); err == nil {
		t.Fatal("expected a finalize error after a write timeout")
	}
	if writes := client.count("write"); writes != 1 {
		t.Fatalf("expected one provider write, got %d", writes)
	}
	if finalizes := client.count("finalize"); finalizes != 0 {
		t.Fatalf("expected finalize to be skipped after a write timeout, got %d calls", finalizes)
	}
}

func TestFinalizeDoesNotHangOnStuckProvider(t *testing.T) {
	client := &stuckClient{finalizeBlock: make(chan struct{})}
	defer close(client.finalizeBlock)
	stream := newStuckStream(client)
	defer stream.Close()

	finalized := make(chan error, 1)
	go func() { finalized <- stream.Finalize() }()
	select {
	case err := <-finalized:
		if err == nil {
			t.Fatal("expected a finalize error while the provider connection is stuck")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finalize did not return while the provider connection was stuck")
	}
}

func TestCloseReturnsWhenProviderStuck(t *testing.T) {
	client := &stuckClient{stopBlock: make(chan struct{})}
	defer close(client.stopBlock)
	stream := newStuckStream(client)

	closed := make(chan error, 1)
	go func() { closed <- stream.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("close did not return while the provider connection was stuck")
	}
	if _, ok := <-stream.Results(); ok {
		t.Fatal("expected the results channel to be closed")
	}
	if _, ok := <-stream.Errors(); ok {
		t.Fatal("expected the errors channel to be closed")
	}
	if stops := client.count("stop"); stops != 1 {
		t.Fatalf("expected one provider stop, got %d", stops)
	}
}

func TestLiveOptions(t *testing.T) {
	options := liveOptions("nova-2", "multi", 48000)
	if options.Model != "nova-2" || options.Encoding != "linear16" || options.SampleRate != 48000 {
		t.Fatalf("unexpected options: %+v", options)
	}
	if options.Language != "multi" {
		t.Fatalf("unexpected language: %q", options.Language)
	}
	if !options.InterimResults || !options.Punctuate || !options.SmartFormat {
		t.Fatalf("expected interim results, punctuation and smart format: %+v", options)
	}
}

func TestToResult(t *testing.T) {
	message := &msginterfaces.MessageResponse{
		IsFinal:  true,
		Start:    2.5,
		Duration: 1.25,
		Channel: msginterfaces.Channel{Alternatives: []msginterfaces.Alternative{{
			Transcript: "selamat pagi",
			Languages:  []string{"id"},
		}}},
	}
	result := toResult(message)
	if result.Text != "selamat pagi" || !result.IsFinal || result.Start != 2.5 || result.Duration != 1.25 || result.Language != "id" {
		t.Fatalf("unexpected result: %+v", result)
	}
	empty := toResult(&msginterfaces.MessageResponse{})
	if empty.Text != "" || empty.IsFinal {
		t.Fatalf("unexpected empty result: %+v", empty)
	}
}
