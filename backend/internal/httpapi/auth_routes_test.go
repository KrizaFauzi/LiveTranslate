package httpapi

import (
	"encoding/json"
	"livetranslate/backend/internal/config"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the public mux: a gateway-only test cannot catch missing route registration.
func TestPublicAuthRoutesAreRegistered(t *testing.T) {
	handler := Handler(config.Config{})
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/auth/register"}, {"POST", "/api/auth/login"},
		{"POST", "/api/auth/refresh"}, {"POST", "/api/auth/logout"}, {"GET", "/api/auth/me"},
	} {
		t.Run(route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(w, req)
			if w.Code != 503 {
				t.Fatalf("unconfigured auth must reach gateway and return 503, got %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("missing auth response headers: %v", w.Header())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code == "" {
				t.Fatalf("expected structured auth error, got %s", w.Body.String())
			}
		})
	}
}
