package occurrence

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type archiveHandler struct {
	repo   repository
	verify func(context.Context, string) (string, *auth.VerificationError)
}

type archiveListCursor struct {
	User      string `json:"user"`
	Committed string `json:"committed"`
	ID        string `json:"id"`
}

func archiveLimit(r *http.Request) (int, bool) {
	values, present := r.URL.Query()["limit"]
	if !present {
		return 20, true
	}
	if len(values) != 1 {
		return 0, false
	}
	value, err := strconv.Atoi(values[0])
	return value, err == nil && value >= 1 && value <= 100
}

func newArchiveHandler(c config.Config) *archiveHandler {
	return &archiveHandler{newRepository(c, nil), auth.NewUserVerifier(c, nil)}
}
func (h *archiveHandler) raw(ctx context.Context, method, path string, q url.Values, body any, token string) ([]byte, *failure) {
	if h.repo.configErr != nil {
		return nil, &failure{503, "supabase_config", h.repo.configErr.Error()}
	}
	target := *h.repo.base
	target.Path = path
	target.RawQuery = q.Encode()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(payload))
	req.Header.Set("apikey", h.repo.key)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := h.repo.client.Do(req)
	if e != nil {
		return nil, &failure{502, "archive_unreachable", "Arsip tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if e != nil || len(data) > 16<<20 {
		return nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &failure{502, "archive_api_error", "Operasi arsip gagal."}
	}
	return data, nil
}
func (h *archiveHandler) archiveForDelete(ctx context.Context, id, token string) (Archive, []string, *failure) {
	data, err := h.raw(ctx, http.MethodGet, "/rest/v1/archives", url.Values{
		"select": {"id,user_id,occurrence_id,manifest,status,committed_at,deleted_at"},
		"id":     {"eq." + id},
		"limit":  {"1"},
	}, nil, token)
	if err != nil {
		return Archive{}, nil, err
	}
	var archives []Archive
	if json.Unmarshal(data, &archives) != nil || len(archives) != 1 ||
		(archives[0].Status != "committed" && archives[0].Status != "partial_failed" && archives[0].Status != "deleted") {
		return Archive{}, nil, &failure{404, "not_found", "Arsip tidak ditemukan."}
	}
	archive := archives[0]
	pathData, err := h.raw(ctx, http.MethodGet, "/rest/v1/documents", url.Values{
		"select":        {"object_path"},
		"occurrence_id": {"eq." + archive.OccurrenceID},
		"order":         {"id.asc"},
		"limit":         {"10"},
	}, nil, token)
	if err != nil {
		return Archive{}, nil, err
	}
	var documents []struct {
		ObjectPath string `json:"object_path"`
	}
	if json.Unmarshal(pathData, &documents) != nil {
		return Archive{}, nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
	}
	paths := make([]string, 0, len(documents))
	prefix := archive.UserID + "/" + archive.OccurrenceID + "/"
	for _, document := range documents {
		if !strings.HasPrefix(document.ObjectPath, prefix) || len(document.ObjectPath) > 1024 {
			return Archive{}, nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
		}
		paths = append(paths, document.ObjectPath)
	}
	return archive, paths, nil
}

func (h *archiveHandler) archiveDetail(ctx context.Context, id, user, token string) (Archive, []archiveDocument, *failure) {
	data, err := h.raw(ctx, http.MethodGet, "/rest/v1/archives", url.Values{
		"select": {"id,user_id,occurrence_id,manifest,status,committed_at,deleted_at"},
		"id":     {"eq." + id},
		"status": {"in.(committed,partial_failed)"},
		"limit":  {"1"},
	}, nil, token)
	if err != nil {
		return Archive{}, nil, err
	}
	var archives []Archive
	if json.Unmarshal(data, &archives) != nil || len(archives) != 1 || archives[0].UserID != user ||
		(archives[0].Status != "committed" && archives[0].Status != "partial_failed") {
		return Archive{}, nil, &failure{404, "not_found", "Arsip tidak ditemukan."}
	}
	archive := archives[0]
	documentData, err := h.raw(ctx, http.MethodGet, "/rest/v1/documents", url.Values{
		"select":        {"id,object_path,file_name,media_type,byte_size,sha256,version,status,error_code,quality_flags"},
		"occurrence_id": {"eq." + archive.OccurrenceID},
		"order":         {"id.asc"},
		"limit":         {"10"},
	}, nil, token)
	if err != nil {
		return Archive{}, nil, err
	}
	var documents []archiveDocument
	if json.Unmarshal(documentData, &documents) != nil || len(documents) > 10 {
		return Archive{}, nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
	}
	prefix := user + "/" + archive.OccurrenceID + "/"
	for _, document := range documents {
		if !uuidPattern.MatchString(document.ID) || !strings.HasPrefix(document.ObjectPath, prefix) ||
			len(document.ObjectPath) > 1024 || len(document.SHA256) != 64 || document.ByteSize < 0 {
			return Archive{}, nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
		}
	}
	return archive, documents, nil
}

func (h *archiveHandler) deleteStorageObjects(ctx context.Context, paths []string, token string) *failure {
	return cleanupStorageObjects(ctx, h.repo, paths, token)
}

func (h *archiveHandler) auth(ctx context.Context, r *http.Request) (string, string, *failure) {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) != 2 {
		return "", "", &failure{401, "unauthorized", "Sesi tidak valid."}
	}
	user, e := h.verify(ctx, f[1])
	if e != nil {
		return "", "", &failure{e.Status, e.Code, e.Message}
	}
	return user, f[1], nil
}
func (h *archiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	user, token, e := h.auth(ctx, r)
	if e != nil {
		writeFailure(w, e)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/archives"), "/")
	if path == "" && r.Method == "GET" {
		limit, valid := archiveLimit(r)
		if !valid {
			writeFailure(w, &failure{400, "invalid_pagination", "Paginasi arsip tidak valid."})
			return
		}
		query := url.Values{
			"select": {"id,user_id,occurrence_id,manifest,status,committed_at,deleted_at"},
			"status": {"in.(committed,partial_failed)"}, "order": {"committed_at.desc,id.desc"},
			"limit": {strconv.Itoa(limit + 1)},
		}
		if values, present := r.URL.Query()["cursor"]; present {
			if len(values) != 1 || len(values[0]) > 2048 {
				writeFailure(w, &failure{400, "invalid_pagination", "Paginasi arsip tidak valid."})
				return
			}
			raw, decodeErr := base64.RawURLEncoding.DecodeString(values[0])
			var cursor archiveListCursor
			committed, timeErr := time.Time{}, error(nil)
			if decodeErr == nil && json.Unmarshal(raw, &cursor) == nil {
				committed, timeErr = time.Parse(time.RFC3339Nano, cursor.Committed)
			}
			if decodeErr != nil || timeErr != nil || cursor.User != user || !uuidPattern.MatchString(cursor.ID) {
				writeFailure(w, &failure{400, "invalid_pagination", "Paginasi arsip tidak valid."})
				return
			}
			cursor.Committed = committed.UTC().Format(time.RFC3339Nano)
			query.Set("or", "(committed_at.lt."+cursor.Committed+",and(committed_at.eq."+cursor.Committed+",id.lt."+cursor.ID+"))")
		}
		data, x := h.raw(ctx, "GET", "/rest/v1/archives", query, nil, token)
		if x != nil {
			writeFailure(w, x)
			return
		}
		var rows []Archive
		if json.Unmarshal(data, &rows) != nil || len(rows) > limit+1 {
			writeFailure(w, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."})
			return
		}
		for _, row := range rows {
			if row.UserID != user || !uuidPattern.MatchString(row.ID) || row.CommittedAt == "" ||
				(row.Status != "committed" && row.Status != "partial_failed") {
				writeFailure(w, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."})
				return
			}
		}
		var next *string
		if len(rows) > limit {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			cursor, _ := json.Marshal(archiveListCursor{User: user, Committed: last.CommittedAt, ID: last.ID})
			value := base64.RawURLEncoding.EncodeToString(cursor)
			next = &value
		}
		writeJSON(w, 200, map[string]any{"archives": rows, "next_cursor": next})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 1 || !uuidPattern.MatchString(parts[0]) {
		writeFailure(w, &failure{404, "not_found", "Arsip tidak ditemukan."})
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		archive, documents, detailErr := h.archiveDetail(ctx, id, user, token)
		if detailErr != nil {
			writeFailure(w, detailErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"archive": archive, "documents": documents})
		return
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		_, paths, lookupErr := h.archiveForDelete(ctx, id, token)
		if lookupErr != nil {
			writeFailure(w, lookupErr)
			return
		}
		data, deleteErr := h.raw(ctx, "POST", "/rest/v1/rpc/delete_meeting_archive", nil, map[string]string{"archive_uuid": id}, token)
		if deleteErr != nil {
			writeFailure(w, deleteErr)
			return
		}
		var deleted bool
		if json.Unmarshal(data, &deleted) != nil || !deleted {
			writeFailure(w, &failure{404, "not_found", "Arsip tidak ditemukan."})
			return
		}
		if cleanupErr := h.deleteStorageObjects(ctx, paths, token); cleanupErr != nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"deleted": true, "storage_cleanup_pending": true})
			return
		}
		w.WriteHeader(204)
		return
	}
	if len(parts) == 2 && parts[1] == "export" && r.Method == "GET" {
		h.export(w, ctx, id, token)
		return
	}
	writeFailure(w, &failure{404, "not_found", "Endpoint tidak tersedia."})
}

type archiveDocument struct {
	ID         string   `json:"id"`
	ObjectPath string   `json:"object_path"`
	FileName   string   `json:"file_name"`
	SHA256     string   `json:"sha256"`
	ByteSize   int64    `json:"byte_size"`
	MediaType  string   `json:"media_type"`
	Version    int      `json:"version"`
	Status     string   `json:"status"`
	ErrorCode  string   `json:"error_code"`
	Quality    []string `json:"quality_flags"`
}

var archiveTableOrder = map[string]string{
	"transcript_segments":       "run_id.asc,seq.asc",
	"translations":              "run_id.asc,source_seq.asc,source_revision.asc",
	"documents":                 "id.asc",
	"document_chunks":           "document_id.asc,chunk_index.asc",
	"questions":                 "created_at.asc,id.asc",
	"answers":                   "created_at.asc,id.asc",
	"assistance_segment_status": "run_id.asc,source_seq.asc",
}

func manifestCounts(archive Archive) (map[string]int, *failure) {
	if archive.Manifest["occurrence_id"] != archive.OccurrenceID {
		return nil, &failure{502, "archive_response_invalid", "Manifest arsip tidak valid."}
	}
	rawObjects, ok := archive.Manifest["objects"].([]any)
	if !ok {
		return nil, &failure{502, "archive_response_invalid", "Manifest arsip tidak valid."}
	}
	counts := make(map[string]int, len(rawObjects))
	for _, raw := range rawObjects {
		object, ok := raw.(map[string]any)
		objectType, typeOK := object["type"].(string)
		countValue, countOK := object["count"].(float64)
		hash, hashOK := object["sha256"].(string)
		orderExists := archiveTableOrder[objectType] != ""
		if !ok || !typeOK || !countOK || !hashOK || !orderExists || countValue < 0 ||
			countValue != float64(int(countValue)) || len(hash) != 64 {
			return nil, &failure{502, "archive_response_invalid", "Manifest arsip tidak valid."}
		}
		counts[objectType] = int(countValue)
	}
	if len(counts) != len(archiveTableOrder) {
		return nil, &failure{502, "archive_response_invalid", "Manifest arsip tidak valid."}
	}
	return counts, nil
}

func (h *archiveHandler) writeTableEntry(ctx context.Context, zw *zip.Writer, table, occurrence, token string, expected int) ([]archiveDocument, *failure) {
	entry, err := zw.Create("data/" + table + ".json")
	if err != nil {
		return nil, &failure{500, "export_failed", "Ekspor gagal dibuat."}
	}
	if _, err = io.WriteString(entry, "["); err != nil {
		return nil, &failure{500, "export_failed", "Ekspor gagal dibuat."}
	}
	const pageSize = 500
	count := 0
	var documents []archiveDocument
	for offset := 0; ; offset += pageSize {
		query := url.Values{
			"select": {"*"}, "occurrence_id": {"eq." + occurrence},
			"order": {archiveTableOrder[table]}, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)},
		}
		data, requestErr := h.raw(ctx, http.MethodGet, "/rest/v1/"+table, query, nil, token)
		if requestErr != nil {
			return nil, requestErr
		}
		var rows []json.RawMessage
		if json.Unmarshal(data, &rows) != nil || len(rows) > pageSize {
			return nil, &failure{502, "archive_response_invalid", "Respons arsip tidak valid."}
		}
		for _, row := range rows {
			if count > 0 {
				if _, err = io.WriteString(entry, ","); err != nil {
					return nil, &failure{500, "export_failed", "Ekspor gagal dibuat."}
				}
			}
			if _, err = entry.Write(row); err != nil {
				return nil, &failure{500, "export_failed", "Ekspor gagal dibuat."}
			}
			count++
			if table == "documents" {
				var document archiveDocument
				if json.Unmarshal(row, &document) != nil || !uuidPattern.MatchString(document.ID) ||
					document.ObjectPath == "" || document.FileName == "" || document.ByteSize < 1 || len(document.SHA256) != 64 {
					return nil, &failure{502, "archive_response_invalid", "Respons dokumen arsip tidak valid."}
				}
				documents = append(documents, document)
			}
		}
		if len(rows) < pageSize {
			break
		}
	}
	if _, err = io.WriteString(entry, "]"); err != nil {
		return nil, &failure{500, "export_failed", "Ekspor gagal dibuat."}
	}
	if count != expected {
		return nil, &failure{502, "export_incomplete", "Jumlah artefak arsip tidak cocok dengan manifest."}
	}
	return documents, nil
}

func (h *archiveHandler) export(w http.ResponseWriter, ctx context.Context, id, token string) {
	archiveData, e := h.raw(ctx, "GET", "/rest/v1/archives", url.Values{
		"select": {"id,user_id,occurrence_id,manifest,status,committed_at,deleted_at"},
		"id":     {"eq." + id}, "status": {"in.(committed,partial_failed)"}, "limit": {"1"},
	}, nil, token)
	if e != nil {
		writeFailure(w, e)
		return
	}
	var archives []Archive
	if json.Unmarshal(archiveData, &archives) != nil || len(archives) != 1 {
		writeFailure(w, &failure{404, "not_found", "Arsip tidak ditemukan."})
		return
	}
	counts, countErr := manifestCounts(archives[0])
	if countErr != nil {
		writeFailure(w, countErr)
		return
	}
	verificationData, verificationErr := h.raw(ctx, http.MethodPost, "/rest/v1/rpc/verify_meeting_archive", nil, map[string]string{"archive_uuid": id}, token)
	if verificationErr != nil {
		writeFailure(w, verificationErr)
		return
	}
	var verified bool
	if json.Unmarshal(verificationData, &verified) != nil || !verified {
		writeFailure(w, &failure{502, "export_incomplete", "Hash artefak arsip tidak cocok dengan manifest."})
		return
	}
	tmp, createErr := os.CreateTemp("", "livetranslate-export-*.zip")
	if createErr != nil {
		writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuat."})
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	zw := zip.NewWriter(tmp)
	meta, createErr := zw.Create("archive.json")
	if createErr == nil {
		createErr = json.NewEncoder(meta).Encode(archives[0])
	}
	if createErr != nil {
		_ = zw.Close()
		_ = tmp.Close()
		writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuat."})
		return
	}
	var documents []archiveDocument
	for _, table := range []string{"transcript_segments", "translations", "documents", "document_chunks", "questions", "answers", "assistance_segment_status"} {
		tableDocuments, tableErr := h.writeTableEntry(ctx, zw, table, archives[0].OccurrenceID, token, counts[table])
		if tableErr != nil {
			_ = zw.Close()
			_ = tmp.Close()
			writeFailure(w, tableErr)
			return
		}
		if table == "documents" {
			documents = tableDocuments
		}
	}
	for _, doc := range documents {
		target := *h.repo.base
		target.Path = "/storage/v1/object/authenticated/meeting-documents/" + doc.ObjectPath
		req, _ := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
		req.Header.Set("apikey", h.repo.key)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := h.repo.client.Do(req)
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp != nil {
				resp.Body.Close()
			}
			if doc.Status == "failed" {
				marker, markerErr := zw.Create(filepath.Join("documents", doc.ID, "source-unavailable.json"))
				if markerErr == nil {
					markerErr = json.NewEncoder(marker).Encode(map[string]string{
						"status": doc.Status, "error_code": doc.ErrorCode,
					})
				}
				if markerErr == nil {
					continue
				}
			}
			_ = zw.Close()
			_ = tmp.Close()
			writeFailure(w, &failure{502, "export_incomplete", "File sumber arsip tidak lengkap."})
			return
		}
		entry, createEntryErr := zw.Create(filepath.Join("documents", doc.ID, filepath.Base(doc.FileName)))
		if createEntryErr != nil {
			resp.Body.Close()
			_ = zw.Close()
			_ = tmp.Close()
			writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuat."})
			return
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(entry, hash), io.LimitReader(resp.Body, (20<<20)+1))
		resp.Body.Close()
		if copyErr != nil || written != doc.ByteSize || written > 20<<20 || hex.EncodeToString(hash.Sum(nil)) != doc.SHA256 {
			_ = zw.Close()
			_ = tmp.Close()
			writeFailure(w, &failure{502, "export_incomplete", "File sumber arsip tidak lengkap atau rusak."})
			return
		}
	}
	if zw.Close() != nil || tmp.Close() != nil {
		writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuat."})
		return
	}
	file, openErr := os.Open(name)
	if openErr != nil {
		writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuka."})
		return
	}
	defer file.Close()
	info, statErr := file.Stat()
	if statErr != nil {
		writeFailure(w, &failure{500, "export_failed", "Ekspor gagal dibuka."})
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="livetranslate-`+id+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.WriteHeader(200)
	_, _ = io.Copy(w, file)
}
