package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/config"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

func authError(w http.ResponseWriter, status int) {
	messages := map[int]string{400: "Permintaan tidak valid.", 401: "Kredensial atau sesi tidak valid. Periksa email konfirmasi jika diperlukan.", 404: "Endpoint tidak ditemukan.", 405: "Metode tidak diizinkan.", 413: "Permintaan terlalu besar.", 415: "Gunakan JSON.", 429: "Terlalu banyak percobaan. Coba lagi nanti.", 502: "Layanan sementara tidak tersedia.", 503: "Autentikasi belum tersedia.", 504: "Layanan tidak merespons."}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": http.StatusText(status), "message": messages[status]}})
}

type rateEntry struct {
	count int
	until time.Time
}

func NewAuthGateway(c config.Config, client *http.Client) http.Handler {
	if client == nil {
		client = &http.Client{Timeout: config.ConnectionTimeout}
	}
	// Never send internal credentials to a redirect destination.
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &copied
	base, parseErr := url.Parse(c.AuthServiceURL)
	configured := parseErr == nil && base != nil && base.Host != "" && (base.Scheme == "http" || base.Scheme == "https") && base.User == nil && base.RawQuery == "" && base.Fragment == "" && (base.Path == "" || base.Path == "/") && c.AuthInternalToken != ""
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
			authError(w, 404)
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			authError(w, 405)
			return
		}
		if !configured {
			authError(w, 503)
			return
		}
		if r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/register" {
			if !allow(r.RemoteAddr) {
				w.Header().Set("Retry-After", "60")
				authError(w, 429)
				return
			}
		}
		var body []byte
		if r.URL.Path == "/api/auth/logout" || r.Method == "GET" {
			if r.ContentLength > 0 {
				authError(w, 400)
				return
			}
		} else {
			content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || content != "application/json" {
				authError(w, 415)
				return
			}
			var readErr error
			body, readErr = io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
			if readErr != nil {
				var limit *http.MaxBytesError
				if errors.As(readErr, &limit) {
					authError(w, 413)
				} else {
					authError(w, 400)
				}
				return
			}
		}
		target := *base
		target.Path = r.URL.Path
		outgoing, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
		if err != nil {
			authError(w, 502)
			return
		}
		outgoing.Header.Set("X-Internal-Token", c.AuthInternalToken)
		if len(body) > 0 {
			outgoing.Header.Set("Content-Type", "application/json")
		}
		if token := r.Header.Get("Authorization"); token != "" {
			if len(token) > 12016 {
				authError(w, 401)
				return
			}
			outgoing.Header.Set("Authorization", token)
		}
		response, err := client.Do(outgoing)
		if err != nil {
			var timeout net.Error
			if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
				authError(w, 504)
			} else {
				authError(w, 502)
			}
			return
		}
		defer response.Body.Close()
		if response.StatusCode >= 400 {
			status := response.StatusCode
			switch status {
			case 400, 401, 413, 415, 429, 503, 504:
			default:
				status = 502
			}
			authError(w, status)
			return
		}
		if response.StatusCode == 204 {
			w.WriteHeader(204)
			return
		}
		if response.StatusCode != 200 && response.StatusCode != 201 && response.StatusCode != 202 {
			authError(w, 502)
			return
		}
		payload, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || len(payload) > 65536 || !json.Valid(payload) {
			authError(w, 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(payload)
	})
}
