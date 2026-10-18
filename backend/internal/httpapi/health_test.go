package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "DEEPGRAM_API_KEY", "SUMOPOD_API_KEY"} {
		t.Setenv(key, "")
	}
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 {
		t.Fatalf("status: %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["status"] != "ok" || got["service"] != "livetranslate-backend" {
		t.Fatalf("body: %v", got)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatal("missing JSON content type")
	}
}

func TestConfirmationLanding(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/confirmed", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(w.Code, w.Header())
	}
	if !strings.Contains(w.Body.String(), "buka kembali extension") || strings.Contains(w.Body.String(), "<script") {
		t.Fatal("confirmation must show instructions without reading tokens")
	}
}
