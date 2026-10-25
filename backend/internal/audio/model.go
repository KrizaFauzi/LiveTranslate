package audio

import "encoding/json"

// nullableTimestamp retains Data API timestamps without extending retention.
type nullableTimestamp string

func (t nullableTimestamp) MarshalJSON() ([]byte, error) {
	if t == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(t))
}

// Run is one capture session bound to a meeting of the authenticated user.
// Audio bytes are streamed and never stored. Transcript storage requires consent.
type Run struct {
	ID                  string            `json:"id"`
	UserID              string            `json:"user_id"`
	MeetingID           string            `json:"meeting_id"`
	OccurrenceID        string            `json:"occurrence_id"`
	Status              string            `json:"status"`
	BytesReceived       int64             `json:"bytes_received"`
	ChunksReceived      int               `json:"chunks_received"`
	StartedAt           string            `json:"started_at"`
	EndedAt             string            `json:"ended_at"`
	SaveTranscript      bool              `json:"save_transcript"`
	TranscriptState     string            `json:"transcript_state"`
	TranscriptExpiresAt nullableTimestamp `json:"transcript_expires_at"`
	Storage             *storageMetadata  `json:"storage,omitempty"`
}
