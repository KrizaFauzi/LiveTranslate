package assistance

import (
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type handler struct {
	base   *url.URL
	key    string
	client *http.Client
	verify func(context.Context, string) (string, *auth.VerificationError)
	err    error
}

func RegisterRoutes(mux *http.ServeMux, c config.Config) {
	base, e := c.Supabase()
	mux.Handle("/api/questions", &handler{base, c.SupabaseKey, &http.Client{Timeout: config.ConnectionTimeout}, auth.NewUserVerifier(c, nil), e})
}
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		write(w, 404, map[string]any{"error": map[string]string{"code": "not_found", "message": "Endpoint tidak tersedia."}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 {
		write(w, 401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "Sesi tidak valid."}})
		return
	}
	if _, e := h.verify(ctx, fields[1]); e != nil {
		write(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message}})
		return
	}
	occurrence := r.URL.Query().Get("occurrence_id")
	if !uuidPattern.MatchString(occurrence) || h.err != nil {
		write(w, 400, map[string]any{"error": map[string]string{"code": "invalid_request", "message": "Occurrence tidak valid."}})
		return
	}
	target := *h.base
	target.Path = "/rest/v1/questions"
	target.RawQuery = url.Values{"select": {"id,text,source_seq,created_at,answers(id,answer_id,answer_en,status,citations,revision_count,updated_at)"}, "occurrence_id": {"eq." + occurrence}, "order": {"created_at.asc,id.asc"}}.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	req.Header.Set("apikey", h.key)
	req.Header.Set("Authorization", "Bearer "+fields[1])
	resp, e := h.client.Do(req)
	if e != nil {
		write(w, 502, map[string]any{"error": map[string]string{"code": "questions_unreachable", "message": "Pertanyaan tidak dapat dimuat."}})
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		write(w, 502, map[string]any{"error": map[string]string{"code": "questions_api_error", "message": "Pertanyaan tidak dapat dimuat."}})
		return
	}
	var rows []map[string]any
	if json.Unmarshal(data, &rows) != nil {
		write(w, 502, map[string]any{"error": map[string]string{"code": "questions_response_invalid", "message": "Respons pertanyaan tidak valid."}})
		return
	}
	write(w, 200, map[string]any{"questions": rows})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
