package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/config"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func writeAuthError(w http.ResponseWriter, failure *authFailure) {
	writeAuthJSON(w, failure.status, map[string]any{"error": map[string]string{"code": failure.code, "message": failure.message}})
}
func writeAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func confirmation(w http.ResponseWriter) {
	writeAuthJSON(w, 202, map[string]string{"message": "Periksa email untuk menyelesaikan pendaftaran, lalu masuk melalui extension."})
}

type rateEntry struct {
	count int
	until time.Time
}

// NewHandler serves the authentication API with the supplied Supabase configuration.
func NewHandler(c config.Config, client *http.Client) http.Handler {
	s := newService(c, client)
	var mu sync.Mutex
	rates := make(map[string]rateEntry)
	allow := func(address string) bool {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		for key, value := range rates {
			if !now.Before(value.until) {
				delete(rates, key)
			}
		}
		entry, exists := rates[host]
		if !exists {
			if len(rates) >= 10000 {
				return false
			}
			entry = rateEntry{until: now.Add(time.Minute)}
		}
		if entry.count >= 10 {
			return false
		}
		entry.count++
		rates[host] = entry
		return true
	}
	routes := map[string]string{"/api/auth/register": "POST", "/api/auth/login": "POST", "/api/auth/refresh": "POST", "/api/auth/logout": "POST", "/api/auth/me": "GET"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), config.ConnectionTimeout)
		defer cancel()
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(config.ConnectionTimeout))
		method, ok := routes[r.URL.Path]
		if !ok {
			writeAuthError(w, &authFailure{404, "not_found", "Endpoint tidak ditemukan."})
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeAuthError(w, &authFailure{405, "method", "Metode tidak diizinkan."})
			return
		}
		if s.configErr != nil {
			writeAuthError(w, &authFailure{503, "supabase_config", s.configErr.Error()})
			return
		}
		register := r.URL.Path == "/api/auth/register"
		if register || r.URL.Path == "/api/auth/login" {
			if !allow(r.RemoteAddr) {
				w.Header().Set("Retry-After", "60")
				writeAuthError(w, &authFailure{429, "rate_limit", "Terlalu banyak percobaan. Coba lagi nanti."})
				return
			}
		}
		var token string
		if r.Method == "GET" || r.URL.Path == "/api/auth/logout" {
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 12000 {
				writeAuthError(w, &authFailure{401, "unauthorized", "Silakan masuk kembali."})
				return
			}
			token = parts[1]
			if b, err := io.ReadAll(io.LimitReader(r.Body, 1)); err != nil || len(b) != 0 {
				writeAuthError(w, invalidInput())
				return
			}
			if r.Method == "GET" {
				user, profile, failure := s.profile(ctx, token)
				if failure != nil {
					writeAuthError(w, failure)
					return
				}
				writeAuthJSON(w, 200, map[string]any{"user": user, "profile": profile})
				return
			}
			if failure := s.logout(ctx, token); failure != nil {
				writeAuthError(w, failure)
				return
			}
			writeAuthJSON(w, 204, nil)
			return
		}
		fields := []string{"email", "password"}
		if register {
			fields = append(fields, "display_name")
		} else if r.URL.Path == "/api/auth/refresh" {
			fields = []string{"refresh_token"}
		}
		input, failure := authInput(w, r, fields)
		if failure != nil {
			writeAuthError(w, failure)
			return
		}
		result, needsConfirmation, failure := s.authenticate(ctx, r.URL.Path, input)
		if failure != nil {
			writeAuthError(w, failure)
			return
		}
		if needsConfirmation {
			confirmation(w)
			return
		}
		status := 200
		if register {
			status = 201
		}
		writeAuthJSON(w, status, map[string]any{"user": result.User, "session": result.authSession})
	})
}

func authInput(w http.ResponseWriter, r *http.Request, fields []string) (map[string]string, *authFailure) {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		return nil, &authFailure{415, "content_type", "Gunakan JSON."}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, &authFailure{413, "body_limit", "Permintaan terlalu besar."}
		}
		return nil, invalidInput()
	}
	var raw map[string]json.RawMessage
	if !utf8.Valid(data) || json.Unmarshal(data, &raw) != nil || len(raw) != len(fields) {
		return nil, invalidInput()
	}
	input := make(map[string]string, len(fields))
	for _, field := range fields {
		var value string
		b, ok := raw[field]
		if !ok || string(b) == "null" || json.Unmarshal(b, &value) != nil {
			return nil, invalidInput()
		}
		input[field] = value
	}
	return input, nil
}

// RegisterRoutes registers all routes owned by the auth feature.
func RegisterRoutes(mux *http.ServeMux, c config.Config) {
	mux.Handle("/api/auth/", NewHandler(c, nil))
	mux.HandleFunc("GET /auth/confirmed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write([]byte("<!doctype html><html lang=\"id\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>LiveTranslate</title><h1>LiveTranslate</h1><p>Setelah email dikonfirmasi, buka kembali extension LiveTranslate dan masuk menggunakan email serta password Anda.</p></html>"))
	})
}
