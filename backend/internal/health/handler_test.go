package health

import (
	"encoding/json"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "DEEPGRAM_API_KEY", "SUMOPOD_API_KEY"} {
		t.Setenv(key, "")
	}
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
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

func TestReadyRequiresValidProviderConfiguration(t *testing.T) {
	base := config.Config{
		SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test",
		DeepgramKey: "deepgram", DeepgramModel: "nova",
		SumopodKey: "sumopod", SumopodModel: "model", SumopodBaseURL: "https://ai.sumopod.com/v1",
	}
	for _, tc := range []struct {
		name string
		cfg  config.Config
		code int
	}{
		{"valid", base, http.StatusOK},
		{"untrusted Sumopod host", func() config.Config { c := base; c.SumopodBaseURL = "https://example.com/v1"; return c }(), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux := http.NewServeMux()
			RegisterRoutes(mux, tc.cfg)
			mux.ServeHTTP(w, httptest.NewRequest("GET", "/ready", nil))
			if w.Code != tc.code {
				t.Fatalf("status: got %d want %d, body=%s", w.Code, tc.code, w.Body.String())
			}
		})
	}
}
