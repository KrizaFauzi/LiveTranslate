package transcribe

// Segment is one transcript update published to the extension.
type Segment struct {
	Seq      int     `json:"seq"`
	Type     string  `json:"type"`
	Text     string  `json:"text"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Language string  `json:"language"`
}

// Result is a raw provider result before the broker assigns a sequence number.
type Result struct {
	Text     string
	IsFinal  bool
	Start    float64
	Duration float64
	Language string
}

func segmentFrom(result Result, seq int) Segment {
	kind := "partial"
	if result.IsFinal {
		kind = "final"
	}
	return Segment{Seq: seq, Type: kind, Text: result.Text, Start: result.Start, Duration: result.Duration, Language: result.Language}
}
