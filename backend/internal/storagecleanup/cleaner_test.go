package storagecleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, value any) *http.Response {
	data, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}
}

func TestRunDeletesPendingObjectsAndCompletesJobs(t *testing.T) {
	const jobID = "aaaaaaaa-2222-4333-8444-555555555555"
	const path = "11111111-2222-4333-8444-555555555555/bbbbbbbb-2222-4333-8444-555555555555/doc/file.pdf"
	deleted := false
	completed := false
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("apikey") != "sb_secret_test" || r.Header.Get("Authorization") != "" {
			t.Fatal("opaque secret must use only the apikey header")
		}
		switch r.URL.Path {
		case "/rest/v1/storage_cleanup_jobs":
			if r.Method == http.MethodGet {
				return response(200, []Job{{ID: jobID, BucketID: "meeting-documents", ObjectPath: path, Attempts: 0}}), nil
			}
			if r.Method == http.MethodPatch {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				completedAt, _ := body["completed_at"].(string)
				completed = body["status"] == "completed" && completedAt != ""
				return response(200, []map[string]string{{"id": jobID}}), nil
			}
		case "/storage/v1/object/meeting-documents":
			deleted = r.Method == http.MethodDelete
			return response(200, []map[string]string{{"name": path}}), nil
		}
		return response(404, map[string]string{}), nil
	})}
	cleaner, err := New("https://project.supabase.co", "sb_secret_test", client)
	if err != nil {
		t.Fatal(err)
	}
	count, err := cleaner.Run(context.Background())
	if err != nil || count != 1 || !deleted || !completed {
		t.Fatalf("count=%d deleted=%v completed=%v err=%v", count, deleted, completed, err)
	}
}

func TestRunKeepsFailedJobsPendingWithAttemptMetadata(t *testing.T) {
	const jobID = "aaaaaaaa-2222-4333-8444-555555555555"
	recorded := false
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/rest/v1/storage_cleanup_jobs" && r.Method == http.MethodGet:
			return response(200, []Job{{ID: jobID, BucketID: "meeting-documents", ObjectPath: "owner/occurrence/document/file.pdf", Attempts: 0}}), nil
		case r.URL.Path == "/storage/v1/object/meeting-documents":
			return response(503, map[string]string{}), nil
		case r.URL.Path == "/rest/v1/storage_cleanup_jobs" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			recorded = body["attempts"] == float64(1) && body["last_error"] == "storage_refused"
			return response(200, []map[string]string{{"id": jobID}}), nil
		}
		return response(404, map[string]string{}), nil
	})}
	cleaner, err := New("https://project.supabase.co", "sb_secret_test", client)
	if err != nil {
		t.Fatal(err)
	}
	if count, runErr := cleaner.Run(context.Background()); runErr == nil || count != 0 || !recorded {
		t.Fatalf("count=%d recorded=%v err=%v", count, recorded, runErr)
	}
}
