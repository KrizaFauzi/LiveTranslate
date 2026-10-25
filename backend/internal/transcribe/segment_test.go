package transcribe

import (
	"encoding/json"
	"testing"
)

func TestSegmentFromResult(t *testing.T) {
	segment := segmentFrom(Result{Text: "halo", IsFinal: true, Start: 1.5, Duration: 0.4, Language: "id"}, 7)
	if segment.Seq != 7 || segment.Type != "final" || segment.Text != "halo" || segment.Start != 1.5 || segment.Duration != 0.4 || segment.Language != "id" {
		t.Fatalf("unexpected segment: %+v", segment)
	}
	partial := segmentFrom(Result{Text: "ha", IsFinal: false}, 8)
	if partial.Type != "partial" {
		t.Fatalf("expected partial, got %q", partial.Type)
	}
}

// TestSegmentJSONShape keeps the wire format of a segment stable: the extension
// reads these keys, so a renamed key would break the transcript of the popup.
func TestSegmentJSONShape(t *testing.T) {
	data, err := json.Marshal(Segment{Seq: 7, Type: "final", Text: "selamat pagi", Start: 12.4, Duration: 1.9, Language: "id"})
	if err != nil {
		t.Fatalf("the segment was not encoded: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("the segment was not decoded: %v", err)
	}
	expected := map[string]any{
		"seq":      float64(7),
		"type":     "final",
		"text":     "selamat pagi",
		"start":    12.4,
		"duration": 1.9,
		"language": "id",
	}
	if len(fields) != len(expected) {
		t.Fatalf("unexpected fields: %s", data)
	}
	for name, want := range expected {
		got, ok := fields[name]
		if !ok {
			t.Fatalf("the field %q is missing: %s", name, data)
		}
		if got != want {
			t.Fatalf("the field %q is %v, expected %v: %s", name, got, want, data)
		}
	}
}
