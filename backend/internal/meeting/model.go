package meeting

type Identity struct {
	Provider       string `json:"provider"`
	ProviderHost   string `json:"provider_host"`
	ProviderRoomID string `json:"provider_room_id"`
	MeetingKey     string `json:"meeting_key"`
	CanonicalURL   string `json:"canonical_url"`
}

type Session struct {
	Identity
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	CreatedAt string `json:"created_at"`
}
