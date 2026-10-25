package documents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var allowed = map[string]bool{"application/pdf": true, "application/vnd.openxmlformats-officedocument.wordprocessingml.document": true, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true, "text/csv": true}

const maxDocumentBytes int64 = 20 << 20
const maxMultipartOverhead int64 = 64 << 10

type handler struct {
	repo   repository
	verify func(context.Context, string) (string, *auth.VerificationError)
}

func NewHandler(c config.Config, client *http.Client) http.Handler {
	return &handler{newRepository(c, client), auth.NewUserVerifier(c, client)}
}
func RegisterRoutes(mux *http.ServeMux, c config.Config) {
	mux.Handle("/api/documents", NewHandler(c, nil))
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeFailure(w http.ResponseWriter, e *failure) {
	writeJSON(w, e.status, map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
}
func (h *handler) identify(ctx context.Context, r *http.Request) (string, string, *failure) {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) != 2 || !strings.EqualFold(f[0], "Bearer") {
		return "", "", &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	u, e := h.verify(ctx, f[1])
	if e != nil {
		return "", "", &failure{e.Status, e.Code, e.Message}
	}
	return u, f[1], nil
}
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		h.upload(w, r)
	case "GET":
		h.list(w, r)
	default:
		writeFailure(w, &failure{404, "not_found", "Endpoint tidak tersedia."})
	}
}
func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	_, token, e := h.identify(ctx, r)
	if e != nil {
		writeFailure(w, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes+maxMultipartOverhead)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(e, &tooLarge) {
			writeFailure(w, &failure{413, "document_too_large", "Dokumen harus berukuran 1 byte sampai 20 MiB."})
		} else {
			writeFailure(w, &failure{400, "invalid_multipart", "Upload dokumen tidak valid."})
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	occurrence := r.FormValue("occurrence_id")
	if !uuidPattern.MatchString(occurrence) {
		writeFailure(w, &failure{400, "invalid_occurrence", "Occurrence tidak valid."})
		return
	}
	file, header, e2 := r.FormFile("file")
	if e2 != nil {
		writeFailure(w, &failure{400, "document_missing", "File dokumen wajib diisi."})
		return
	}
	defer file.Close()
	data, readErr := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if readErr != nil || len(data) == 0 || int64(len(data)) > maxDocumentBytes {
		writeFailure(w, &failure{413, "document_too_large", "Dokumen harus berukuran 1 byte sampai 20 MiB."})
		return
	}
	media := strings.TrimSpace(header.Header.Get("Content-Type"))
	if !allowed[media] {
		writeFailure(w, &failure{415, "document_type_unsupported", "Gunakan PDF, DOCX, XLSX, atau CSV."})
		return
	}
	sum := sha256.Sum256(data)
	doc, createErr := h.repo.create(ctx, occurrence, header.Filename, media, int64(len(data)), hex.EncodeToString(sum[:]), token)
	if createErr != nil {
		writeFailure(w, createErr)
		return
	}
	if uploadErr := h.repo.upload(ctx, doc, data, token); uploadErr != nil {
		h.repo.fail(context.WithoutCancel(ctx), doc.ID, uploadErr.code, token)
		writeFailure(w, uploadErr)
		return
	}
	extraction, extractErr := Extract(ctx, media, data)
	if extractErr != nil {
		h.repo.fail(context.WithoutCancel(ctx), doc.ID, extractErr.Error(), token)
		writeFailure(w, &failure{422, "document_extraction_failed", "Dokumen tersimpan tetapi ekstraksi gagal."})
		return
	}
	ready, commitErr := h.repo.commit(ctx, doc.ID, extraction, token)
	if commitErr != nil {
		h.repo.fail(context.WithoutCancel(ctx), doc.ID, commitErr.code, token)
		writeFailure(w, commitErr)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"document": ready, "chunks": len(extraction.Chunks)})
}
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, token, e := h.identify(ctx, r)
	if e != nil {
		writeFailure(w, e)
		return
	}
	occurrence := r.URL.Query().Get("occurrence_id")
	if !uuidPattern.MatchString(occurrence) {
		writeFailure(w, &failure{400, "invalid_occurrence", "Occurrence tidak valid."})
		return
	}
	rows, listErr := h.repo.list(ctx, occurrence, token)
	if listErr != nil {
		writeFailure(w, listErr)
		return
	}
	writeJSON(w, 200, map[string]any{"documents": rows})
}
