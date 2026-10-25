package occurrence

import (
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFailure(w http.ResponseWriter, err *failure) {
	writeJSON(w, err.status, map[string]any{"error": map[string]string{"code": err.code, "message": err.message}})
}

func validate(row Occurrence, user string) *failure {
	if !uuidPattern.MatchString(row.ID) || !uuidPattern.MatchString(row.MeetingID) || row.UserID != user || row.StartedAt == "" || row.CreatedAt == "" {
		return &failure{502, "occurrence_response_invalid", "Respons occurrence tidak valid."}
	}
	switch row.Status {
	case "pending_documents", "active", "awaiting_decision", "discarding", "archived", "discarded":
	default:
		return &failure{502, "occurrence_response_invalid", "Respons occurrence tidak valid."}
	}
	return nil
}

func NewHandler(c config.Config, client *http.Client) http.Handler {
	repo := newRepository(c, client)
	verify := auth.NewUserVerifier(c, client)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), config.ConnectionTimeout)
		defer cancel()
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
		path := strings.TrimPrefix(r.URL.Path, "/api/occurrences/")
		if path == "connect" {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeFailure(w, &failure{405, "method", "Metode tidak diizinkan."})
				return
			}
			if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/json" {
				writeFailure(w, &failure{415, "content_type", "Gunakan application/json."})
				return
			}
			var input struct {
				MeetingID string `json:"meeting_id"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF || !uuidPattern.MatchString(input.MeetingID) {
				writeFailure(w, &failure{400, "invalid_input", "Meeting tidak valid."})
				return
			}
			rows, err := repo.rpc(ctx, "connect_meeting_occurrence", input.MeetingID, fields[1])
			respondOne(w, rows, err, user, http.StatusCreated)
			return
		}

		parts := strings.Split(path, "/")
		if len(parts) < 1 || !uuidPattern.MatchString(parts[0]) {
			writeFailure(w, &failure{404, "not_found", "Occurrence tidak ditemukan."})
			return
		}
		id := parts[0]
		if len(parts) == 1 && r.Method == http.MethodGet {
			rows, err := repo.request(ctx, http.MethodGet, "/rest/v1/meeting_occurrences", url.Values{"select": {columns}, "id": {"eq." + id}, "limit": {"1"}}, nil, fields[1])
			respondOne(w, rows, err, user, http.StatusOK)
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost && (parts[1] == "activate" || parts[1] == "finish") {
			rows, err := repo.rpc(ctx, parts[1]+"_meeting_occurrence", id, fields[1])
			respondOne(w, rows, err, user, http.StatusOK)
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "discard" {
			paths, pathErr := repo.occurrenceObjectPaths(ctx, id, user, fields[1])
			if pathErr != nil {
				writeFailure(w, pathErr)
				return
			}
			rows, err := repo.rpc(ctx, "discard_meeting_occurrence", id, fields[1])
			if err != nil {
				writeFailure(w, err)
				return
			}
			if len(rows) != 1 || validate(rows[0], user) != nil {
				respondOne(w, rows, nil, user, http.StatusOK)
				return
			}
			cleanupPending := cleanupStorageObjects(ctx, repo, paths, fields[1]) != nil
			writeJSON(w, http.StatusOK, map[string]any{
				"occurrence": rows[0], "server_time": time.Now().UTC().Format(time.RFC3339Nano),
				"storage_cleanup_pending": cleanupPending,
			})
			return
		}
		if len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "archive" {
			archive, err := repo.commitArchive(ctx, id, fields[1])
			if err != nil {
				writeFailure(w, err)
				return
			}
			if archive.UserID != user || archive.OccurrenceID != id ||
				!uuidPattern.MatchString(archive.ID) || (archive.Status != "committed" && archive.Status != "partial_failed") ||
				archive.CommittedAt == "" || archive.Manifest == nil {
				writeFailure(w, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"archive": archive})
			return
		}
		writeFailure(w, &failure{404, "not_found", "Endpoint tidak tersedia."})
	})
}

func respondOne(w http.ResponseWriter, rows []Occurrence, err *failure, user string, status int) {
	if err != nil {
		writeFailure(w, err)
		return
	}
	if len(rows) != 1 || validate(rows[0], user) != nil {
		if len(rows) == 0 {
			writeFailure(w, &failure{404, "not_found", "Occurrence tidak ditemukan."})
			return
		}
		writeFailure(w, &failure{502, "occurrence_response_invalid", "Respons occurrence tidak valid."})
		return
	}
	writeJSON(w, status, map[string]any{"occurrence": rows[0], "server_time": time.Now().UTC().Format(time.RFC3339Nano)})
}

func RegisterRoutes(mux *http.ServeMux, c config.Config) {
	mux.Handle("/api/occurrences/", NewHandler(c, nil))
	mux.Handle("/api/archives", newArchiveHandler(c))
	mux.Handle("/api/archives/", newArchiveHandler(c))
}
