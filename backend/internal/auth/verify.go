package auth

import (
	"context"
	"encoding/json"
	"livetranslate/backend/internal/config"
	"net/http"
)

// VerificationError exposes a safe HTTP error without provider response bodies.
type VerificationError struct {
	Status        int
	Code, Message string
}

func (e *VerificationError) Error() string { return e.Message }

// NewUserVerifier validates access tokens with Supabase Auth; it never trusts JWT claims locally.
func NewUserVerifier(c config.Config, client *http.Client) func(context.Context, string) (string, *VerificationError) {
	repo, configErr := newRepository(c, client)
	return func(ctx context.Context, token string) (string, *VerificationError) {
		if configErr != nil {
			return "", &VerificationError{503, "supabase_config", configErr.Error()}
		}
		payload, status, failure := repo.request(ctx, "GET", "/auth/v1/user", nil, nil, token, "auth")
		if failure != nil {
			return "", &VerificationError{failure.status, failure.code, failure.message}
		}
		if status != 200 {
			failure = upstreamAuthError(status, false, nil)
			return "", &VerificationError{failure.status, failure.code, failure.message}
		}
		var user authUser
		if json.Unmarshal(payload, &user) != nil || !uuidPattern.MatchString(user.ID) {
			return "", &VerificationError{502, "auth_response_invalid", "Respons identitas pengguna tidak valid."}
		}
		return user.ID, nil
	}
}
