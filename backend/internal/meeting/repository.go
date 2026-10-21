package meeting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/url"
)

type repository struct {
	base      *url.URL
	key       string
	client    *http.Client
	configErr error
}
type failure struct {
	status        int
	code, message string
}

func (e *failure) Error() string { return e.message }
func newRepository(c config.Config, client *http.Client) repository {
	if client == nil {
		client = &http.Client{Timeout: config.ConnectionTimeout}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, err := c.Supabase()
	return repository{base, c.SupabaseKey, &copied, err}
}

const columns = "id,user_id,provider,provider_host,provider_room_id,meeting_key,canonical_url,created_at"

func (repo repository) request(ctx context.Context, method string, query url.Values, body any, token string) ([]Session, *failure) {
	if repo.configErr != nil {
		return nil, &failure{503, "supabase_config", repo.configErr.Error()}
	}
	target := *repo.base
	target.Path = "/rest/v1/meeting_sessions"
	target.RawQuery = query.Encode()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, &failure{500, "internal_error", "Permintaan tidak dapat dibuat."}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, &failure{500, "internal_error", "Permintaan tidak dapat dibuat."}
	}
	req.Header.Set("apikey", repo.key)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "resolution=ignore-duplicates,return=representation")
	}
	resp, err := repo.client.Do(req)
	if err != nil {
		status := 502
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = 504
		}
		return nil, &failure{status, "meeting_unreachable", "Database meeting tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) >= 1<<20 {
		return nil, &failure{502, "meeting_response_invalid", "Respons database meeting tidak valid."}
	}
	if resp.StatusCode == 401 {
		return nil, &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, &failure{502, "meeting_api_error", "Akses database meeting gagal. Periksa migrasi 0002, grants, dan RLS."}
	}
	var rows []Session
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		return nil, &failure{502, "meeting_response_invalid", "Respons database meeting tidak valid."}
	}
	return rows, nil
}
func validateRows(rows []Session, user string) *failure {
	for _, row := range rows {
		identity, err := classifyURL(row.CanonicalURL)
		if err != nil || identity != row.Identity || row.UserID != user || !uuidPattern.MatchString(row.ID) || row.CreatedAt == "" {
			return &failure{502, "meeting_response_invalid", "Respons database meeting tidak valid."}
		}
	}
	return nil
}
func (repo repository) resolve(ctx context.Context, identity Identity, user, token string) (Session, *failure) {
	body := map[string]string{"user_id": user, "provider": identity.Provider, "provider_host": identity.ProviderHost, "provider_room_id": identity.ProviderRoomID}
	query := url.Values{"select": {columns}, "on_conflict": {"user_id,meeting_key"}}
	rows, err := repo.request(ctx, "POST", query, body, token)
	if err != nil {
		return Session{}, err
	}
	if len(rows) == 0 {
		rows, err = repo.request(ctx, "GET", url.Values{"select": {columns}, "user_id": {"eq." + user}, "meeting_key": {"eq." + identity.MeetingKey}, "limit": {"1"}}, nil, token)
		if err != nil {
			return Session{}, err
		}
	}
	if err := validateRows(rows, user); err != nil {
		return Session{}, err
	}
	if len(rows) != 1 || rows[0].Identity != identity {
		return Session{}, &failure{502, "meeting_response_invalid", "Sesi meeting tidak dapat diselesaikan."}
	}
	return rows[0], nil
}
