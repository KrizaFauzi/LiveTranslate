package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/config"
	"net"
	"net/http"
	"net/url"
)

type repository struct {
	base   *url.URL
	key    string
	client *http.Client
}

// request only sends server-selected headers; redirects never receive credentials.
func (a repository) request(ctx context.Context, method, path string, query url.Values, body any, token, stage string) ([]byte, int, *authFailure) {
	target := *a.base
	target.Path = path
	target.RawQuery = query.Encode()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, 0, &authFailure{500, "internal_error", "Permintaan tidak dapat dibuat."}
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(data))
	if err != nil {
		return nil, 0, &authFailure{502, stage + "_api_error", "Permintaan Supabase tidak dapat dibuat."}
	}
	req.Header.Set("apikey", a.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			return nil, 0, &authFailure{504, stage + "_timeout", "Supabase " + stage + " tidak merespons dalam batas waktu."}
		}
		return nil, 0, &authFailure{502, stage + "_unreachable", "Supabase " + stage + " tidak dapat dijangkau. Periksa koneksi server dan SUPABASE_URL."}
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(payload) > 65536 {
		return nil, resp.StatusCode, &authFailure{502, stage + "_response_invalid", "Respons Supabase " + stage + " tidak valid."}
	}
	return payload, resp.StatusCode, nil
}

func newRepository(c config.Config, client *http.Client) (repository, error) {
	if client == nil {
		client = &http.Client{Timeout: config.ConnectionTimeout}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	base, configErr := c.Supabase()
	return repository{base: base, key: c.SupabaseKey, client: &copied}, configErr
}
