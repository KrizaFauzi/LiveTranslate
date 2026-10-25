package documents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"livetranslate/backend/internal/config"
)

type Document struct {
	ID           string   `json:"id"`
	UserID       string   `json:"user_id"`
	MeetingID    string   `json:"meeting_id"`
	OccurrenceID string   `json:"occurrence_id"`
	FileName     string   `json:"file_name"`
	MediaType    string   `json:"media_type"`
	ByteSize     int64    `json:"byte_size"`
	SHA256       string   `json:"sha256"`
	Version      int      `json:"version"`
	ObjectPath   string   `json:"object_path"`
	Status       string   `json:"status"`
	ErrorCode    *string  `json:"error_code"`
	QualityFlags []string `json:"quality_flags"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}
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
		client = &http.Client{Timeout: config.AITimeout}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, e := c.Supabase()
	return repository{base, c.SupabaseKey, &copied, e}
}
func (r repository) json(ctx context.Context, method, path string, query url.Values, body any, token string) ([]byte, *failure) {
	if r.configErr != nil {
		return nil, &failure{503, "supabase_config", r.configErr.Error()}
	}
	target := *r.base
	target.Path = path
	target.RawQuery = query.Encode()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
	if e != nil {
		return nil, &failure{500, "internal_error", "Permintaan dokumen tidak dapat dibuat."}
	}
	req.Header.Set("apikey", r.key)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "return=representation")
	}
	resp, e := r.client.Do(req)
	if e != nil {
		status := 502
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = 504
		}
		return nil, &failure{status, "document_unreachable", "Penyimpanan dokumen tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, re := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if re != nil || len(data) > 64<<20 {
		return nil, &failure{502, "document_response_invalid", "Respons dokumen tidak valid."}
	}
	if resp.StatusCode == 401 {
		return nil, &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &failure{502, "document_api_error", "Operasi dokumen gagal."}
	}
	return data, nil
}
func (r repository) decode(data []byte) (Document, *failure) {
	var rows []Document
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 || rows[0].ID == "" || rows[0].OccurrenceID == "" {
		return Document{}, &failure{502, "document_response_invalid", "Respons dokumen tidak valid."}
	}
	return rows[0], nil
}
func (r repository) create(ctx context.Context, occurrence, name, media string, size int64, sha, token string) (Document, *failure) {
	d, e := r.json(ctx, "POST", "/rest/v1/rpc/create_meeting_document", nil, map[string]any{"occurrence_uuid": occurrence, "document_name": name, "document_media_type": media, "document_size": size, "document_sha256": sha}, token)
	if e != nil {
		return Document{}, e
	}
	return r.decode(d)
}
func (r repository) commit(ctx context.Context, id string, x Extraction, token string) (Document, *failure) {
	d, e := r.json(ctx, "POST", "/rest/v1/rpc/commit_document_extraction", nil, map[string]any{"document_uuid": id, "extraction_chunks": x.Chunks, "extraction_flags": x.QualityFlags}, token)
	if e != nil {
		return Document{}, e
	}
	return r.decode(d)
}
func (r repository) fail(ctx context.Context, id, code, token string) {
	_, _ = r.json(ctx, "POST", "/rest/v1/rpc/fail_document_extraction", nil, map[string]any{"document_uuid": id, "failure_code": code}, token)
}
func (r repository) upload(ctx context.Context, d Document, data []byte, token string) *failure {
	target := *r.base
	target.Path = "/storage/v1/object/meeting-documents/" + strings.Join(strings.Split(d.ObjectPath, "/"), "/")
	req, e := http.NewRequestWithContext(ctx, "POST", target.String(), bytes.NewReader(data))
	if e != nil {
		return &failure{500, "internal_error", "Upload tidak dapat dibuat."}
	}
	req.Header.Set("apikey", r.key)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", d.MediaType)
	req.Header.Set("x-upsert", "false")
	resp, e := r.client.Do(req)
	if e != nil {
		return &failure{502, "document_storage_unreachable", "Penyimpanan file tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &failure{502, "document_storage_error", "File gagal disimpan."}
	}
	return nil
}
func (r repository) list(ctx context.Context, occurrence, token string) ([]Document, *failure) {
	q := url.Values{"select": {"id,user_id,meeting_id,occurrence_id,file_name,media_type,byte_size,sha256,version,object_path,status,error_code,quality_flags,created_at,updated_at"}, "occurrence_id": {"eq." + occurrence}, "order": {"created_at.asc"}}
	data, e := r.json(ctx, "GET", "/rest/v1/documents", q, nil, token)
	if e != nil {
		return nil, e
	}
	var rows []Document
	if json.Unmarshal(data, &rows) != nil {
		return nil, &failure{502, "document_response_invalid", "Respons dokumen tidak valid."}
	}
	return rows, nil
}
