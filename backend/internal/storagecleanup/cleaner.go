package storagecleanup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const batchSize = 100

type Job struct {
	ID         string `json:"id"`
	BucketID   string `json:"bucket_id"`
	ObjectPath string `json:"object_path"`
	Attempts   int    `json:"attempts"`
}

type Cleaner struct {
	base   *url.URL
	secret string
	client *http.Client
}

func New(rawURL, secret string, client *http.Client) (*Cleaner, error) {
	base, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || base.Hostname() == "" || base.User != nil || base.Fragment != "" {
		return nil, errors.New("SUPABASE_URL tidak valid")
	}
	local := base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1"
	if base.Scheme != "https" && !(base.Scheme == "http" && local) {
		return nil, errors.New("SUPABASE_URL harus HTTPS atau HTTP localhost")
	}
	secret = strings.TrimSpace(secret)
	if !validServerSecret(secret) {
		return nil, errors.New("SUPABASE_SECRET_KEY harus secret key atau legacy service_role key")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Cleaner{base: base, secret: secret, client: &copied}, nil
}

func validServerSecret(secret string) bool {
	if strings.HasPrefix(secret, "sb_secret_") && !strings.ContainsAny(secret, " \t\r\n") {
		return true
	}
	parts := strings.Split(secret, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Role string `json:"role"`
	}
	return err == nil && json.Unmarshal(payload, &claims) == nil && claims.Role == "service_role"
}

func (c *Cleaner) request(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	target := *c.base
	target.Path = path
	target.RawQuery = query.Encode()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.secret)
	if !strings.HasPrefix(c.secret, "sb_secret_") {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.client.Do(req)
}

func (c *Cleaner) pending(ctx context.Context) ([]Job, error) {
	resp, err := c.request(ctx, http.MethodGet, "/rest/v1/storage_cleanup_jobs", url.Values{
		"select": {"id,bucket_id,object_path,attempts"}, "status": {"eq.pending"},
		"order": {"created_at.asc,id.asc"}, "limit": {fmt.Sprint(batchSize)},
	}, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if readErr != nil || len(data) > 2<<20 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("cleanup worklist tidak dapat dibaca")
	}
	var jobs []Job
	if json.Unmarshal(data, &jobs) != nil || len(jobs) > batchSize {
		return nil, errors.New("cleanup worklist tidak valid")
	}
	for _, job := range jobs {
		if job.ID == "" || job.BucketID != "meeting-documents" || job.ObjectPath == "" || len(job.ObjectPath) > 1024 || strings.ContainsRune(job.ObjectPath, 0) || job.Attempts < 0 {
			return nil, errors.New("cleanup worklist tidak valid")
		}
	}
	return jobs, nil
}

func (c *Cleaner) recordFailures(ctx context.Context, jobs []Job, code string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, job := range jobs {
		query := url.Values{"id": {"eq." + job.ID}, "status": {"eq.pending"}}
		resp, err := c.request(ctx, http.MethodPatch, "/rest/v1/storage_cleanup_jobs", query, map[string]any{
			"attempts": job.Attempts + 1, "last_error": code, "updated_at": now,
		})
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
		}
	}
}

func (c *Cleaner) Run(ctx context.Context) (int, error) {
	jobs, err := c.pending(ctx)
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	paths := make([]string, 0, len(jobs))
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		paths = append(paths, job.ObjectPath)
		ids = append(ids, job.ID)
	}
	resp, err := c.request(ctx, http.MethodDelete, "/storage/v1/object/meeting-documents", nil, map[string]any{"prefixes": paths})
	if err != nil {
		c.recordFailures(ctx, jobs, "storage_unreachable")
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.recordFailures(ctx, jobs, "storage_refused")
		return 0, errors.New("Storage API menolak cleanup")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := url.Values{"id": {"in.(" + strings.Join(ids, ",") + ")"}, "status": {"eq.pending"}}
	resp, err = c.request(ctx, http.MethodPatch, "/rest/v1/storage_cleanup_jobs", query, map[string]any{
		"status": "completed", "completed_at": now, "updated_at": now, "last_error": nil,
	})
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, errors.New("status cleanup tidak dapat disimpan")
	}
	return len(jobs), nil
}
