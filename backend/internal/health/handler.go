package health

import (
	"encoding/json"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/sumopod"
	"net/http"
)

// RegisterRoutes registers the backend liveness endpoint.
func RegisterRoutes(mux *http.ServeMux, c ...config.Config) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "livetranslate-backend"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ready := len(c) == 1
		if ready {
			cfg := c[0]
			_, supabaseErr := cfg.Supabase()
			ready = supabaseErr == nil && config.Require("DEEPGRAM_API_KEY", cfg.DeepgramKey) == nil &&
				config.Require("DEEPGRAM_MODEL", cfg.DeepgramModel) == nil &&
				config.Require("SUMOPOD_API_KEY", cfg.SumopodKey) == nil &&
				config.Require("SUMOPOD_MODEL", cfg.SumopodModel) == nil &&
				sumopod.ValidEndpoint(cfg.SumopodBaseURL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready})
	})
}
