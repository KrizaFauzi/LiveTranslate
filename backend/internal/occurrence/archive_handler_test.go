package occurrence

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
)

func TestArchiveDeleteUsesStorageAPIAndCompletesDurableCleanup(t *testing.T) {
	objectPath := ownerID + "/" + occurrenceID + "/dddddddd-2222-4333-8444-555555555555/brief.csv"
	var deletedPrefixes []string
	completed := false
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rest/v1/archives":
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: map[string]any{"schema_version": float64(2)}, Status: "committed", CommittedAt: "2026-10-24T00:00:00Z"}}), nil
		case "/rest/v1/documents":
			return response(200, []map[string]any{{"object_path": objectPath}}), nil
		case "/rest/v1/rpc/delete_meeting_archive":
			return response(200, true), nil
		case "/storage/v1/object/meeting-documents":
			if r.Method != http.MethodDelete {
				t.Fatalf("storage method = %s", r.Method)
			}
			var body struct {
				Prefixes []string `json:"prefixes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			deletedPrefixes = body.Prefixes
			return response(200, []map[string]string{{"name": objectPath}}), nil
		case "/rest/v1/rpc/complete_storage_cleanup_jobs":
			completed = true
			return response(200, 1), nil
		default:
			return response(404, map[string]string{}), nil
		}
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodDelete, "/api/archives/"+archiveID, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if len(deletedPrefixes) != 1 || deletedPrefixes[0] != objectPath || !completed {
		t.Fatalf("cleanup incomplete: prefixes=%v completed=%v", deletedPrefixes, completed)
	}
}

func TestArchiveListUsesOwnerBoundKeysetPagination(t *testing.T) {
	secondID := "dddddddd-2222-4333-8444-555555555555"
	rows := []Archive{
		{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: map[string]any{}, Status: "committed", CommittedAt: "2026-10-24T02:00:00Z"},
		{ID: secondID, UserID: ownerID, OccurrenceID: "eeeeeeee-2222-4333-8444-555555555555", Manifest: map[string]any{}, Status: "partial_failed", CommittedAt: "2026-10-24T01:00:00Z"},
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/v1/archives" || r.URL.Query().Get("status") != "in.(committed,partial_failed)" ||
			r.URL.Query().Get("order") != "committed_at.desc,id.desc" || r.URL.Query().Get("limit") != "2" {
			return response(500, map[string]string{}), nil
		}
		if r.URL.Query().Get("or") != "" {
			return response(200, rows[1:]), nil
		}
		return response(200, rows), nil
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	first := call(h, http.MethodGet, "/api/archives?limit=1", "")
	var page struct {
		Archives []Archive `json:"archives"`
		Next     *string   `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if first.Code != 200 || len(page.Archives) != 1 || page.Next == nil {
		t.Fatalf("first page: %d %s", first.Code, first.Body.String())
	}
	second := call(h, http.MethodGet, "/api/archives?limit=1&cursor="+*page.Next, "")
	page = struct {
		Archives []Archive `json:"archives"`
		Next     *string   `json:"next_cursor"`
	}{}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if second.Code != 200 || len(page.Archives) != 1 || page.Archives[0].ID != secondID || page.Next != nil {
		t.Fatalf("second page: %d %s", second.Code, second.Body.String())
	}
}

func TestArchiveExportPaginatesEveryTable(t *testing.T) {
	objects := []map[string]any{}
	tables := []string{"transcript_segments", "translations", "documents", "document_chunks", "questions", "answers", "assistance_segment_status"}
	for _, table := range tables {
		count := 0
		if table == "transcript_segments" {
			count = 501
		}
		objects = append(objects, map[string]any{"type": table, "count": count, "sha256": strings.Repeat("a", 64)})
	}
	manifest := map[string]any{"schema_version": float64(2), "occurrence_id": occurrenceID, "objects": objects}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/v1/archives" {
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: manifest, Status: "committed", CommittedAt: "2026-10-24T00:00:00Z"}}), nil
		}
		if r.URL.Path == "/rest/v1/rpc/verify_meeting_archive" {
			return response(200, true), nil
		}
		for _, table := range tables {
			if r.URL.Path != "/rest/v1/"+table {
				continue
			}
			if r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("order") == "" {
				return response(500, map[string]string{}), nil
			}
			if table != "transcript_segments" {
				return response(200, []map[string]any{}), nil
			}
			offset := r.URL.Query().Get("offset")
			start := 0
			if offset == "500" {
				start = 500
			}
			end := start + 500
			if end > 501 {
				end = 501
			}
			rows := make([]map[string]any, 0, end-start)
			for i := start; i < end; i++ {
				rows = append(rows, map[string]any{"seq": i + 1, "text": fmt.Sprintf("segment-%d", i+1)})
			}
			return response(200, rows), nil
		}
		return response(404, map[string]string{}), nil
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodGet, "/api/archives/"+archiveID+"/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name != "data/transcript_segments.json" {
			continue
		}
		rc, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		var rows []map[string]any
		if json.Unmarshal(data, &rows) != nil || len(rows) != 501 {
			t.Fatalf("exported transcript count=%d", len(rows))
		}
		return
	}
	t.Fatal("paginated transcript entry missing")
}

func TestArchiveExportRejectsManifestHashMismatch(t *testing.T) {
	objects := []map[string]any{}
	for table := range archiveTableOrder {
		objects = append(objects, map[string]any{"type": table, "count": 0, "sha256": strings.Repeat("a", 64)})
	}
	manifest := map[string]any{"schema_version": float64(2), "occurrence_id": occurrenceID, "objects": objects}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rest/v1/archives":
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: manifest, Status: "committed", CommittedAt: "2026-10-24T00:00:00Z"}}), nil
		case "/rest/v1/rpc/verify_meeting_archive":
			return response(200, false), nil
		}
		if strings.HasPrefix(r.URL.Path, "/rest/v1/") {
			return response(200, []map[string]any{}), nil
		}
		return response(404, map[string]string{}), nil
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodGet, "/api/archives/"+archiveID+"/export", "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "export_incomplete") {
		t.Fatalf("hash mismatch exported: %d %s", w.Code, w.Body.String())
	}
}

func TestArchiveDetailReturnsManifestAndDocumentMetadata(t *testing.T) {
	objectPath := ownerID + "/" + occurrenceID + "/dddddddd-2222-4333-8444-555555555555/brief.csv"
	manifest := map[string]any{"schema_version": float64(2), "occurrence_id": occurrenceID}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rest/v1/archives":
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: manifest, Status: "committed", CommittedAt: "2026-10-24T00:00:00Z"}}), nil
		case "/rest/v1/documents":
			return response(200, []map[string]any{{"id": "dddddddd-2222-4333-8444-555555555555", "object_path": objectPath, "file_name": "brief.csv", "sha256": strings.Repeat("d", 64), "byte_size": 12, "quality_flags": []string{"formula_cached"}}}), nil
		default:
			return response(404, map[string]string{}), nil
		}
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodGet, "/api/archives/"+archiveID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Archive   Archive           `json:"archive"`
		Documents []archiveDocument `json:"documents"`
	}
	if json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.Archive.ID != archiveID ||
		len(payload.Documents) != 1 || payload.Documents[0].FileName != "brief.csv" ||
		len(payload.Documents[0].Quality) != 1 || payload.Documents[0].Quality[0] != "formula_cached" {
		t.Fatalf("unexpected detail: %s", w.Body.String())
	}
}

func TestArchiveDeleteReportsDurablePendingCleanup(t *testing.T) {
	objectPath := ownerID + "/" + occurrenceID + "/dddddddd-2222-4333-8444-555555555555/brief.csv"
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rest/v1/archives":
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Status: "committed"}}), nil
		case "/rest/v1/documents":
			return response(200, []map[string]any{{"object_path": objectPath}}), nil
		case "/rest/v1/rpc/delete_meeting_archive":
			return response(200, true), nil
		case "/storage/v1/object/meeting-documents":
			return response(503, map[string]string{}), nil
		default:
			return response(404, map[string]string{}), nil
		}
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodDelete, "/api/archives/"+archiveID, "")
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"storage_cleanup_pending":true`) {
		t.Fatalf("pending cleanup response: %d %s", w.Code, w.Body.String())
	}
}

func TestPartialArchiveExportMarksUnavailableFailedDocument(t *testing.T) {
	documentID := "dddddddd-2222-4333-8444-555555555555"
	objectPath := ownerID + "/" + occurrenceID + "/" + documentID + "/failed.pdf"
	objects := []map[string]any{}
	for table := range archiveTableOrder {
		count := 0
		if table == "documents" {
			count = 1
		}
		objects = append(objects, map[string]any{"type": table, "count": count, "sha256": strings.Repeat("a", 64)})
	}
	manifest := map[string]any{"schema_version": float64(2), "occurrence_id": occurrenceID, "objects": objects}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rest/v1/archives":
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: manifest, Status: "partial_failed"}}), nil
		case "/rest/v1/rpc/verify_meeting_archive":
			return response(200, true), nil
		case "/rest/v1/documents":
			return response(200, []map[string]any{{"id": documentID, "object_path": objectPath, "file_name": "failed.pdf", "sha256": strings.Repeat("d", 64), "byte_size": 12, "status": "failed", "error_code": "document_storage_error", "quality_flags": []string{}}}), nil
		case "/storage/v1/object/authenticated/meeting-documents/" + objectPath:
			return response(404, map[string]string{}), nil
		}
		if strings.HasPrefix(r.URL.Path, "/rest/v1/") {
			return response(200, []map[string]any{}), nil
		}
		return response(404, map[string]string{}), nil
	})}
	h := &archiveHandler{
		repo:   newRepository(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client),
		verify: func(context.Context, string) (string, *auth.VerificationError) { return ownerID, nil },
	}
	w := call(h, http.MethodGet, "/api/archives/"+archiveID+"/export", "")
	if w.Code != http.StatusOK {
		t.Fatalf("partial export: %d %s", w.Code, w.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range reader.File {
		if file.Name == "documents/"+documentID+"/source-unavailable.json" {
			return
		}
	}
	t.Fatal("failed source marker missing")
}
