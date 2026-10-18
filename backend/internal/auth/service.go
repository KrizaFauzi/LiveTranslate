package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type service struct {
	config     config.Config
	repository repository
	configErr  error
}

func newService(c config.Config, client *http.Client) *service {
	repository, err := newRepository(c, client)
	return &service{config: c, repository: repository, configErr: err}
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var emailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func invalidInput() *authFailure {
	return &authFailure{400, "invalid_input", "Periksa field, email, password (8–128 karakter), dan nama (1–100 karakter)."}
}

func upstreamAuthError(status int, register bool, payload []byte) *authFailure {
	if register {
		var detail struct {
			ErrorCode string `json:"error_code"`
			Code      string `json:"code"`
		}
		_ = json.Unmarshal(payload, &detail)
		if detail.ErrorCode == "user_already_exists" || detail.Code == "user_already_exists" {
			return nil
		}
	}
	switch status {
	case 429:
		return &authFailure{429, "rate_limit", "Terlalu banyak percobaan. Coba lagi nanti."}
	case 400, 401, 403, 422:
		if register {
			return &authFailure{400, "registration_failed", "Pendaftaran gagal. Periksa data dan kebijakan password Supabase."}
		}
		return &authFailure{401, "unauthorized", "Kredensial atau sesi tidak valid. Periksa email konfirmasi jika diperlukan."}
	default:
		return &authFailure{502, "auth_api_error", fmt.Sprintf("Supabase Auth mengembalikan HTTP %d. Periksa Project URL, API key, dan status layanan.", status)}
	}
}

func (s *service) authenticate(ctx context.Context, operation string, input map[string]string) (*tokenResponse, bool, *authFailure) {
	register := operation == "/api/auth/register"
	path := "/auth/v1/token"
	query := url.Values{}
	body := map[string]any{}
	if operation == "/api/auth/refresh" {
		if strings.TrimSpace(input["refresh_token"]) == "" || len(input["refresh_token"]) > 12000 {
			return nil, false, invalidInput()
		}
		query.Set("grant_type", "refresh_token")
		body["refresh_token"] = input["refresh_token"]
	} else {
		email := strings.TrimSpace(input["email"])
		length := utf8.RuneCountInString(input["password"])
		if len(email) > 254 || !emailPattern.MatchString(email) || length < 8 || length > 128 {
			return nil, false, invalidInput()
		}
		body["email"] = email
		body["password"] = input["password"]
		query.Set("grant_type", "password")
		if register {
			name := strings.TrimSpace(input["display_name"])
			if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
				return nil, false, invalidInput()
			}
			if err := s.config.AuthRedirect(); err != nil {
				return nil, false, &authFailure{503, "supabase_config", err.Error()}
			}
			path = "/auth/v1/signup"
			query = url.Values{"redirect_to": {s.config.SupabaseAuthRedirectURL}}
			body["data"] = map[string]string{"display_name": name}
		}
	}
	payload, status, failure := s.repository.request(ctx, "POST", path, query, body, "", "auth")
	if failure != nil {
		return nil, false, failure
	}
	if status != 200 && status != 201 {
		failure = upstreamAuthError(status, register, payload)
		return nil, failure == nil, failure
	}
	var result tokenResponse
	if json.Unmarshal(payload, &result) != nil {
		return nil, false, &authFailure{502, "auth_response_invalid", "Respons Supabase Auth tidak valid."}
	}
	if register && result.AccessToken == "" && (uuidPattern.MatchString(result.ID) || uuidPattern.MatchString(result.User.ID)) {
		return nil, true, nil
	}
	if result.ExpiresAt == 0 && result.ExpiresIn > 0 && result.ExpiresIn <= 86400*365 {
		result.ExpiresAt = time.Now().Unix() + result.ExpiresIn
	}
	if result.AccessToken == "" || result.RefreshToken == "" || result.ExpiresAt <= time.Now().Unix() || !strings.EqualFold(result.TokenType, "bearer") || !uuidPattern.MatchString(result.User.ID) {
		return nil, false, &authFailure{502, "auth_response_invalid", "Respons Supabase Auth tidak berisi sesi valid."}
	}
	return &result, false, nil
}

func (s *service) profile(ctx context.Context, token string) (authUser, authProfile, *authFailure) {
	payload, status, failure := s.repository.request(ctx, "GET", "/auth/v1/user", nil, nil, token, "auth")
	if failure != nil {
		return authUser{}, authProfile{}, failure
	}
	if status != 200 {
		return authUser{}, authProfile{}, upstreamAuthError(status, false, nil)
	}
	var user authUser
	if json.Unmarshal(payload, &user) != nil || !uuidPattern.MatchString(user.ID) {
		return authUser{}, authProfile{}, &authFailure{502, "auth_response_invalid", "Respons pengguna Supabase Auth tidak valid."}
	}
	query := url.Values{"select": {"id,display_name,created_at,updated_at"}, "id": {"eq." + user.ID}, "limit": {"1"}}
	payload, status, failure = s.repository.request(ctx, "GET", "/rest/v1/profiles", query, nil, token, "profile")
	if failure != nil {
		return authUser{}, authProfile{}, failure
	}
	if status == 401 {
		return authUser{}, authProfile{}, upstreamAuthError(status, false, nil)
	}
	if status != 200 {
		return authUser{}, authProfile{}, &authFailure{502, "profile_api_error", fmt.Sprintf("Supabase Data API mengembalikan HTTP %d. Periksa Data API, tabel public.profiles, grants, dan RLS.", status)}
	}
	var profiles []authProfile
	if json.Unmarshal(payload, &profiles) != nil || string(bytes.TrimSpace(payload)) == "null" {
		return authUser{}, authProfile{}, &authFailure{502, "profile_response_invalid", "Respons profile Supabase tidak valid."}
	}
	if len(profiles) == 0 {
		return authUser{}, authProfile{}, &authFailure{503, "profile_missing", "Profile pengguna tidak ditemukan atau tidak terlihat oleh RLS. Terapkan migrasi profiles dan periksa policy SELECT pengguna sendiri."}
	}
	profile := profiles[0]
	if len(profiles) != 1 || profile.ID != user.ID || strings.TrimSpace(profile.DisplayName) == "" || profile.CreatedAt == "" || profile.UpdatedAt == "" {
		return authUser{}, authProfile{}, &authFailure{502, "profile_response_invalid", "Respons profile Supabase tidak valid."}
	}
	return user, profile, nil
}

func (s *service) logout(ctx context.Context, token string) *authFailure {
	_, status, failure := s.repository.request(ctx, "POST", "/auth/v1/logout", url.Values{"scope": {"local"}}, nil, token, "auth")
	if failure != nil {
		return failure
	}
	if status != 204 {
		return upstreamAuthError(status, false, nil)
	}
	return nil
}
