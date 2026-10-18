package health

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes registers the backend liveness endpoint.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "livetranslate-backend"})
	})
}
