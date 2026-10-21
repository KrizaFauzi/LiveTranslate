package transcribe

import "testing"

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
