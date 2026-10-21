package audio

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

const columns = "id,user_id,meeting_id,status,bytes_received,chunks_received,started_at,ended_at"

// send performs one Data API call. Audio bytes never reach this layer; only run metadata is stored.
func (repo repository) send(ctx context.Context, method, path string, query url.Values, body any, token string) ([]byte, *failure) {
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
		return nil, &failure{status, "audio_unreachable", "Database run audio tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) >= 1<<20 {
		return nil, &failure{502, "audio_response_invalid", "Respons database run audio tidak valid."}
	}
	switch resp.StatusCode {
	case 401:
		return nil, &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	case 404, 409:
		return nil, &failure{404, "not_found", "Sesi meeting tidak ditemukan."}
	}
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, &failure{502, "audio_api_error", "Akses database run audio gagal. Periksa migrasi 0003, grants, dan RLS."}
	}
	return data, nil
}

func decodeRuns(data []byte, user string) ([]Run, *failure) {
	var rows []Run
	if json.Unmarshal(data, &rows) != nil {
		return nil, &failure{502, "audio_response_invalid", "Respons database run audio tidak valid."}
	}
	for _, row := range rows {
		if row.UserID != user || !uuidPattern.MatchString(row.ID) {
			return nil, &failure{502, "audio_response_invalid", "Respons database run audio tidak valid."}
		}
	}
	return rows, nil
}

// meetingOwned checks the meeting through the owner-scoped table so a run can never point at another account.
func (repo repository) meetingOwned(ctx context.Context, meetingID, token string) (bool, *failure) {
	data, err := repo.send(ctx, "GET", "/rest/v1/meeting_sessions", url.Values{"select": {"id"}, "id": {"eq." + meetingID}}, nil, token)
	if err != nil {
		return false, err
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return false, &failure{502, "audio_response_invalid", "Respons database meeting tidak valid."}
	}
	return len(rows) == 1 && rows[0].ID == meetingID, nil
}

func (repo repository) create(ctx context.Context, meetingID, user, token string) (Run, *failure) {
	body := map[string]string{"user_id": user, "meeting_id": meetingID}
	data, err := repo.send(ctx, "POST", "/rest/v1/meeting_runs", url.Values{"select": {columns}}, body, token)
	if err != nil {
		return Run{}, err
	}
	rows, err := decodeRuns(data, user)
	if err != nil {
		return Run{}, err
	}
	if len(rows) != 1 || rows[0].MeetingID != meetingID {
		return Run{}, &failure{502, "audio_response_invalid", "Respons database run audio tidak valid."}
	}
	return rows[0], nil
}

func (repo repository) get(ctx context.Context, id, user, token string) (Run, bool, *failure) {
	data, err := repo.send(ctx, "GET", "/rest/v1/meeting_runs", url.Values{"select": {columns}, "id": {"eq." + id}}, nil, token)
	if err != nil {
		return Run{}, false, err
	}
	rows, err := decodeRuns(data, user)
	if err != nil {
		return Run{}, false, err
	}
	if len(rows) == 0 {
		return Run{}, false, nil
	}
	return rows[0], true, nil
}

// appendChunk accounts one uploaded chunk atomically inside the database.
func (repo repository) appendChunk(ctx context.Context, id string, size int, user, token string) (Run, bool, *failure) {
	body := map[string]any{"run_uuid": id, "chunk_bytes": size}
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/append_audio_chunk", nil, body, token)
	if err != nil {
		return Run{}, false, err
	}
	rows, err := decodeRuns(data, user)
	if err != nil {
		return Run{}, false, err
	}
	if len(rows) == 0 {
		return Run{}, false, nil
	}
	return rows[0], true, nil
}

// markFailed marks a run whose transcription session could not start as failed.
func (repo repository) markFailed(ctx context.Context, id, user, token string) *failure {
	body := map[string]string{"status": "failed"}
	data, err := repo.send(ctx, "PATCH", "/rest/v1/meeting_runs", url.Values{"select": {columns}, "id": {"eq." + id}}, body, token)
	if err != nil {
		return err
	}
	if _, err := decodeRuns(data, user); err != nil {
		return err
	}
	return nil
}

// stopRun closes an active run. A run that is already stopped is returned unchanged.
func (repo repository) stopRun(ctx context.Context, id, user, token string) (Run, bool, *failure) {
	body := map[string]any{"run_uuid": id}
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/stop_audio_run", nil, body, token)
	if err != nil {
		return Run{}, false, err
	}
	rows, err := decodeRuns(data, user)
	if err != nil {
		return Run{}, false, err
	}
	if len(rows) == 0 {
		return repo.get(ctx, id, user, token)
	}
	return rows[0], true, nil
}
