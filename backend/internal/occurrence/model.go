package occurrence

// Occurrence is one actual meeting for one owner. A reusable room can have many occurrences.
type Occurrence struct {
	ID               string  `json:"id"`
	UserID           string  `json:"user_id"`
	MeetingID        string  `json:"meeting_id"`
	Status           string  `json:"status"`
	StartedAt        string  `json:"started_at"`
	EndedAt          *string `json:"ended_at"`
	StagingExpiresAt *string `json:"staging_expires_at"`
	ConsentGivenAt   *string `json:"consent_given_at"`
	CreatedAt        string  `json:"created_at"`
}

type Archive struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	OccurrenceID string         `json:"occurrence_id"`
	Manifest     map[string]any `json:"manifest"`
	Status       string         `json:"status"`
	CommittedAt  string         `json:"committed_at"`
	DeletedAt    *string        `json:"deleted_at"`
}
