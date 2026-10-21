package main

import (
	"encoding/json"
	"livetranslate/backend/internal/config"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the public mux: a gateway-only test cannot catch missing route registration.
func TestPublicAuthRoutesAreRegistered(t *testing.T) {
	handler := newHandler(config.Config{})
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

func TestConfirmationLanding(t *testing.T) {
	w := httptest.NewRecorder()
	newHandler(config.Config{}).ServeHTTP(w, httptest.NewRequest("GET", "/auth/confirmed", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(w.Code, w.Header())
	}
	if !strings.Contains(w.Body.String(), "buka kembali extension") || strings.Contains(w.Body.String(), "<script") {
		t.Fatal("confirmation must show instructions without reading tokens")
	}
}

func TestPublicHealthRouteIsRegistered(t *testing.T) {
	w := httptest.NewRecorder()
	newHandler(config.Config{}).ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body) != 2 || body["status"] != "ok" || body["service"] != "livetranslate-backend" {
		t.Fatalf("unexpected health response: %s", w.Body.String())
	}
}

func TestPublicMeetingRoutesAreRegistered(t *testing.T) {
	handler := newHandler(config.Config{})
	for _, route := range []struct{ method, path string }{{"POST", "/api/meetings/resolve"}, {"GET", "/api/meetings"}, {"GET", "/api/meetings/aaaaaaaa-2222-4333-8444-555555555555"}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("route %s: %d %s", route.path, w.Code, w.Body.String())
		}
	}
}

// The capture routes have to be mounted on the mux, so a client without a
// session gets the session error instead of a missing route.
func TestPublicAudioRoutesAreRegistered(t *testing.T) {
	handler := newHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"})
	for _, route := range []struct{ method, path string }{
		{"POST", "/api/audio/runs"},
		{"GET", "/api/audio/runs/11111111-2222-4333-8444-555555555555/transcript"},
	} {
		t.Run(route.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("route %s must ask for a session instead of missing: %d %s", route.path, w.Code, w.Body.String())
			}
		})
	}
}
