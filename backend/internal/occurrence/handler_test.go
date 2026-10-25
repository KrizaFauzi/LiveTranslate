package occurrence

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"livetranslate/backend/internal/config"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	ownerID      = "11111111-2222-4333-8444-555555555555"
	meetingID    = "aaaaaaaa-2222-4333-8444-555555555555"
	occurrenceID = "bbbbbbbb-2222-4333-8444-555555555555"
	archiveID    = "cccccccc-2222-4333-8444-555555555555"
)

func response(status int, value any) *http.Response {
	data, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}
}

func testOccurrence(status string) Occurrence {
	return Occurrence{ID: occurrenceID, UserID: ownerID, MeetingID: meetingID, Status: status, StartedAt: "2026-10-24T00:00:00Z", CreatedAt: "2026-10-24T00:00:00Z"}
}

func call(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer owner")
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, r)
	return w
}

func TestOccurrenceLifecycleAndArchiveEndpoints(t *testing.T) {
	statuses := map[string]string{
		"connect_meeting_occurrence":  "pending_documents",
		"activate_meeting_occurrence": "active",
		"finish_meeting_occurrence":   "awaiting_decision",
		"discard_meeting_occurrence":  "discarded",
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/auth/v1/user" {
			return response(200, map[string]string{"id": ownerID}), nil
		}
		if r.URL.Path == "/rest/v1/documents" {
			return response(200, []map[string]string{}), nil
		}
		for rpc, status := range statuses {
			if r.URL.Path == "/rest/v1/rpc/"+rpc {
				return response(200, []Occurrence{testOccurrence(status)}), nil
			}
		}
		if r.URL.Path == "/rest/v1/rpc/commit_meeting_archive" {
			return response(200, []Archive{{ID: archiveID, UserID: ownerID, OccurrenceID: occurrenceID, Manifest: map[string]any{"schema_version": float64(1)}, Status: "committed", CommittedAt: "2026-10-24T00:00:00Z"}}), nil
		}
		if r.URL.Path == "/rest/v1/meeting_occurrences" {
			return response(200, []Occurrence{testOccurrence("active")}), nil
		}
		return response(404, map[string]string{}), nil
	})}
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client)
	cases := []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/occurrences/connect", `{"meeting_id":"` + meetingID + `"}`, 201},
		{"POST", "/api/occurrences/" + occurrenceID + "/activate", `{}`, 200},
		{"POST", "/api/occurrences/" + occurrenceID + "/finish", `{}`, 200},
		{"GET", "/api/occurrences/" + occurrenceID, "", 200},
		{"POST", "/api/occurrences/" + occurrenceID + "/archive", `{}`, 200},
		{"POST", "/api/occurrences/" + occurrenceID + "/discard", `{}`, 200},
	}
	for _, tc := range cases {
		w := call(h, tc.method, tc.path, tc.body)
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestOccurrenceRejectsCrossOwnerDataAPIResponse(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/auth/v1/user" {
			return response(200, map[string]string{"id": ownerID}), nil
		}
		row := testOccurrence("pending_documents")
		row.UserID = "66666666-2222-4333-8444-555555555555"
		return response(200, []Occurrence{row}), nil
	})}
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client)
	w := call(h, "POST", "/api/occurrences/connect", `{"meeting_id":"`+meetingID+`"}`)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "occurrence_response_invalid") {
		t.Fatalf("foreign response accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestOccurrenceDiscardUsesStorageAPI(t *testing.T) {
	objectPath := ownerID + "/" + occurrenceID + "/dddddddd-2222-4333-8444-555555555555/brief.csv"
	storageDeleted := false
	cleanupCompleted := false
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/auth/v1/user":
			return response(200, map[string]string{"id": ownerID}), nil
		case "/rest/v1/documents":
			return response(200, []map[string]string{{"object_path": objectPath}}), nil
		case "/rest/v1/rpc/discard_meeting_occurrence":
			return response(200, []Occurrence{testOccurrence("discarded")}), nil
		case "/storage/v1/object/meeting-documents":
			storageDeleted = r.Method == http.MethodDelete
			return response(200, []map[string]string{{"name": objectPath}}), nil
		case "/rest/v1/rpc/complete_storage_cleanup_jobs":
			cleanupCompleted = true
			return response(200, 1), nil
		default:
			return response(404, map[string]string{}), nil
		}
	})}
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client)
	w := call(h, http.MethodPost, "/api/occurrences/"+occurrenceID+"/discard", `{}`)
	if w.Code != http.StatusOK || !storageDeleted || !cleanupCompleted {
		t.Fatalf("discard cleanup: %d storage=%v completed=%v body=%s", w.Code, storageDeleted, cleanupCompleted, w.Body.String())
	}
}
