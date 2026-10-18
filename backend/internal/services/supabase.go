package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/url"
)

func CheckSupabase(ctx context.Context, c config.Config) error {
	if err := config.Require("AUTH_SERVICE_URL", c.AuthServiceURL); err != nil {
		return err
	}
	if err := config.Require("AUTH_INTERNAL_TOKEN", c.AuthInternalToken); err != nil {
		return err
	}
	target, err := url.Parse(c.AuthServiceURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Path != "" && target.Path != "/") {
		return errors.New("AUTH_SERVICE_URL tidak valid")
	}
	target.Path = "/internal/database-check"
	ctx, cancel := context.WithTimeout(ctx, config.ConnectionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return errors.New("Supabase: pemeriksaan gagal")
	}
	req.Header.Set("X-Internal-Token", c.AuthInternalToken)
	client := &http.Client{Timeout: config.ConnectionTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("Supabase: service Drizzle tidak dapat dijangkau")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Supabase: pemeriksaan Drizzle gagal")
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&result) != nil || result.Status != "ok" {
		return errors.New("Supabase: respons pemeriksaan tidak valid")
	}
	return nil
}
