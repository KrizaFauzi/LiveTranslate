package audio

// Run is one capture session bound to a meeting of the authenticated user.
// Audio bytes are streamed through the backend and never stored; only these counters persist.
type Run struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	MeetingID      string `json:"meeting_id"`
	Status         string `json:"status"`
	BytesReceived  int64  `json:"bytes_received"`
	ChunksReceived int    `json:"chunks_received"`
	StartedAt      string `json:"started_at"`
	EndedAt        string `json:"ended_at"`
}
