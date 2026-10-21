package meeting

import (
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeFailure(w http.ResponseWriter, e *failure) {
	writeJSON(w, e.status, map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
}
func NewHandler(c config.Config, client *http.Client) http.Handler {
	repo := newRepository(c, client)
	verify := auth.NewUserVerifier(c, client)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), config.ConnectionTimeout)
		defer cancel()
		path := r.URL.Path
		resolve := path == "/api/meetings/resolve"
		list := path == "/api/meetings"
		id := strings.TrimPrefix(path, "/api/meetings/")
		detail := !resolve && strings.HasPrefix(path, "/api/meetings/") && uuidPattern.MatchString(id)
		if !resolve && !list && !detail {
			writeFailure(w, &failure{404, "not_found", "Endpoint tidak ditemukan."})
			return
		}
		method := "GET"
		if resolve {
			method = "POST"
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeFailure(w, &failure{405, "method", "Metode tidak diizinkan."})
			return
		}
		fields := strings.Fields(r.Header.Get("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 12000 {
			writeFailure(w, &failure{401, "unauthorized", "Silakan masuk terlebih dahulu."})
			return
		}
		user, verifyErr := verify(ctx, fields[1])
		if verifyErr != nil {
			writeFailure(w, &failure{verifyErr.Status, verifyErr.Code, verifyErr.Message})
			return
		}
		if resolve {
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if media != "application/json" {
				writeFailure(w, &failure{415, "content_type", "Gunakan application/json."})
				return
			}
			var input struct {
				CanonicalURL string `json:"canonical_url"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
				writeFailure(w, &failure{400, "invalid_input", "Input meeting tidak valid."})
				return
			}
			identity, err := classifyURL(input.CanonicalURL)
			if err != nil {
				writeFailure(w, &failure{400, "unsupported_meeting", err.Error()})
				return
			}
			session, failure := repo.resolve(ctx, identity, user, fields[1])
			if failure != nil {
				writeFailure(w, failure)
				return
			}
			writeJSON(w, 200, map[string]any{"meeting": session})
			return
		}
		query := url.Values{"select": {columns}, "user_id": {"eq." + user}}
		if detail {
			query.Set("id", "eq."+id)
			query.Set("limit", "1")
		} else {
			limit := 50
			offset := 0
			for name, target := range map[string]*int{"limit": &limit, "offset": &offset} {
				if value := r.URL.Query().Get(name); value != "" {
					parsed, err := strconv.Atoi(value)
					if err != nil || parsed < 0 || (name == "limit" && (parsed < 1 || parsed > 100)) || parsed > 1000000 {
						writeFailure(w, &failure{400, "invalid_input", "Parameter halaman tidak valid."})
						return
					}
					*target = parsed
				}
			}
			query.Set("limit", strconv.Itoa(limit))
			query.Set("offset", strconv.Itoa(offset))
			query.Set("order", "created_at.desc,id.desc")
		}
		rows, err := repo.request(ctx, "GET", query, nil, fields[1])
		if err == nil {
			err = validateRows(rows, user)
		}
		if err != nil {
			writeFailure(w, err)
			return
		}
		if detail {
			if len(rows) != 1 || !strings.EqualFold(rows[0].ID, id) {
				writeFailure(w, &failure{404, "not_found", "Meeting tidak ditemukan."})
				return
			}
			writeJSON(w, 200, map[string]any{"meeting": rows[0]})
			return
		}
		writeJSON(w, 200, map[string]any{"meetings": rows})
	})
}
func RegisterRoutes(mux *http.ServeMux, c config.Config) {
	handler := NewHandler(c, nil)
	mux.Handle("/api/meetings", handler)
	mux.Handle("/api/meetings/", handler)
}
