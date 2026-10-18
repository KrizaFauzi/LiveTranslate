package httpapi

import (
	"encoding/json"
	"livetranslate/backend/internal/config"
	"net/http"
)

func Handler(options ...config.Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/confirmed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, _ = w.Write([]byte("<!doctype html><html lang=\"id\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>LiveTranslate</title><h1>LiveTranslate</h1><p>Setelah email dikonfirmasi, buka kembali extension LiveTranslate dan masuk menggunakan email serta password Anda.</p></html>"))
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "livetranslate-backend"})
	})
	c := config.Load()
	if len(options) > 0 {
		c = options[0]
	}
	mux.Handle("/api/auth/", NewAuthGateway(c, nil))
	return mux
}
