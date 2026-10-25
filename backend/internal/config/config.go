package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const ConnectionTimeout = 10 * time.Second
const AITimeout = 30 * time.Second

type Config struct {
	SupabaseURL             string
	SupabaseKey             string
	SupabaseAuthRedirectURL string
	DeepgramKey             string
	DeepgramModel           string
	DeepgramLanguage        string
	SumopodBaseURL          string
	SumopodKey              string
	SumopodModel            string
}

func Load() Config {
	base := os.Getenv("SUMOPOD_BASE_URL")
	if base == "" {
		base = "https://ai.sumopod.com/v1"
	}
	return Config{
		SupabaseURL: os.Getenv("SUPABASE_URL"), SupabaseKey: os.Getenv("SUPABASE_PUBLISHABLE_KEY"),
		SupabaseAuthRedirectURL: os.Getenv("SUPABASE_AUTH_REDIRECT_URL"), DeepgramKey: os.Getenv("DEEPGRAM_API_KEY"),
		DeepgramModel: os.Getenv("DEEPGRAM_MODEL"), DeepgramLanguage: os.Getenv("DEEPGRAM_LANGUAGE"),
		SumopodBaseURL: base,
		SumopodKey:     os.Getenv("SUMOPOD_API_KEY"), SumopodModel: os.Getenv("SUMOPOD_MODEL"),
	}
}

func Require(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("konfigurasi wajib: %s", name)
	}
	return nil
}

// Supabase parses the configured endpoint and validates an RLS-preserving API key.
func (c Config) Supabase() (*url.URL, error) {
	if err := Require("SUPABASE_URL", c.SupabaseURL); err != nil {
		return nil, err
	}
	u, err := url.Parse(c.SupabaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, errors.New("SUPABASE_URL harus berupa origin tanpa path, kredensial, query, atau fragment")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("SUPABASE_URL harus HTTPS atau HTTP loopback untuk lokal")
	}
	if err := Require("SUPABASE_PUBLISHABLE_KEY", c.SupabaseKey); err != nil {
		return nil, err
	}
	key := c.SupabaseKey
	valid := strings.HasPrefix(key, "sb_publishable_")
	// Legacy anon keys are supported, but service_role/secret keys bypass RLS.
	if parts := strings.Split(key, "."); len(parts) == 3 {
		payload, err := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Role string `json:"role"`
		}
		valid = err == nil && json.Unmarshal(payload, &claims) == nil && claims.Role == "anon"
	}
	if !valid || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("SUPABASE_PUBLISHABLE_KEY harus publishable key atau legacy anon key; jangan gunakan secret/service_role")
	}
	return u, nil
}

func (c Config) AuthRedirect() error {
	if err := Require("SUPABASE_AUTH_REDIRECT_URL", c.SupabaseAuthRedirectURL); err != nil {
		return err
	}
	u, err := url.Parse(c.SupabaseAuthRedirectURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("SUPABASE_AUTH_REDIRECT_URL tidak valid")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("SUPABASE_AUTH_REDIRECT_URL harus HTTPS, atau HTTP localhost untuk lokal")
	}
	return nil
}
