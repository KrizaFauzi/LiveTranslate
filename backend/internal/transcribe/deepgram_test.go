package transcribe

import (
	"testing"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

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
		IsFinal: true,
		Start:   2.5,
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
