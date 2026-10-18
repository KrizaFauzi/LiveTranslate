package auth

type authFailure struct {
	status        int
	code, message string
}

type authUser struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
}
type authSession struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	TokenType    string `json:"token_type"`
}
type tokenResponse struct {
	authSession
	ExpiresIn int64    `json:"expires_in"`
	User      authUser `json:"user"`
	// Signup without a session returns a user object directly.
	ID string `json:"id"`
}
type authProfile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}
