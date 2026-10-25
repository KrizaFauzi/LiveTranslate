package occurrence

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

const columns = "id,user_id,meeting_id,status,started_at,ended_at,staging_expires_at,consent_given_at,created_at"

type failure struct {
	status        int
	code, message string
}

func (e *failure) Error() string { return e.message }

type repository struct {
	base      *url.URL
	key       string
	client    *http.Client
	configErr error
}

func newRepository(c config.Config, client *http.Client) repository {
	if client == nil {
		client = &http.Client{Timeout: config.ConnectionTimeout}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, err := c.Supabase()
	return repository{base: base, key: c.SupabaseKey, client: &copied, configErr: err}
}

func (repo repository) request(ctx context.Context, method, path string, query url.Values, body any, token string) ([]Occurrence, *failure) {
	if repo.configErr != nil {
		return nil, &failure{503, "supabase_config", repo.configErr.Error()}
	}
	target := *repo.base
	target.Path = path
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
		req.Header.Set("Prefer", "return=representation")
	}
	resp, err := repo.client.Do(req)
	if err != nil {
		status := 502
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = 504
		}
		return nil, &failure{status, "occurrence_unreachable", "Database occurrence tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if readErr != nil || len(data) > 1<<20 {
		return nil, &failure{502, "occurrence_response_invalid", "Respons occurrence tidak valid."}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, &failure{502, "occurrence_api_error", "Akses database occurrence gagal. Periksa migrasi 0006 dan RLS."}
	}
	var rows []Occurrence
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		return nil, &failure{502, "occurrence_response_invalid", "Respons occurrence tidak valid."}
	}
	return rows, nil
}

func (repo repository) rpc(ctx context.Context, name, id, token string) ([]Occurrence, *failure) {
	key := "occurrence_uuid"
	if name == "connect_meeting_occurrence" {
		key = "meeting_uuid"
	}
	return repo.request(ctx, http.MethodPost, "/rest/v1/rpc/"+name, nil, map[string]string{key: id}, token)
}

func (repo repository) commitArchive(ctx context.Context, id, token string) (Archive, *failure) {
	if repo.configErr != nil {
		return Archive{}, &failure{503, "supabase_config", repo.configErr.Error()}
	}
	target := *repo.base
	target.Path = "/rest/v1/rpc/commit_meeting_archive"
	payload, _ := json.Marshal(map[string]string{"occurrence_uuid": id})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return Archive{}, &failure{500, "internal_error", "Permintaan tidak dapat dibuat."}
	}
	req.Header.Set("apikey", repo.key)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := repo.client.Do(req)
	if err != nil {
		status := 502
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = 504
		}
		return Archive{}, &failure{status, "archive_unreachable", "Database arsip tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if readErr != nil || len(data) > 1<<20 {
		return Archive{}, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return Archive{}, &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	if resp.StatusCode != http.StatusOK {
		return Archive{}, &failure{502, "archive_api_error", "Commit arsip gagal."}
	}
	var rows []Archive
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 {
		if len(rows) == 0 {
			return Archive{}, &failure{409, "archive_unavailable", "Occurrence belum siap disimpan atau staging sudah kedaluwarsa."}
		}
		return Archive{}, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
	}
	return rows[0], nil
}
