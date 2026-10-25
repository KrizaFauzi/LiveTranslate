package meeting

import (
	"encoding/json"
	"io"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const owner = "11111111-2222-4333-8444-555555555555"
const other = "66666666-2222-4333-8444-555555555555"
const meetingID = "aaaaaaaa-2222-4333-8444-555555555555"

func response(status int, value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}
func request(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestResolveAndOwnership(t *testing.T) {
	var mu sync.Mutex
	rows := map[string]Session{}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		user := owner
		if token == "other" {
			user = other
		}
		if r.Header.Get("apikey") != "sb_publishable_test" {
			t.Error("missing publishable key")
		}
		if r.URL.Path == "/auth/v1/user" {
			if token == "invalid" {
				return response(401, map[string]string{}), nil
			}
			return response(200, map[string]string{"id": user}), nil
		}
		if token == "invalid" {
			t.Fatal("unverified token reached Data API")
		}
		if r.URL.Path != "/rest/v1/meeting_sessions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "POST" {
			if r.Header.Get("Prefer") != "resolution=ignore-duplicates,return=representation" || r.URL.Query().Get("on_conflict") != "user_id,meeting_key" {
				t.Error("insert must ignore concurrent duplicates")
			}
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			if len(input) != 4 || input["user_id"] != user {
				t.Fatalf("untrusted insert %+v", input)
			}
			identity, _ := classifyURL("https://meet.google.com/abc-defg-hij")
			key := user + identity.MeetingKey
			if _, ok := rows[key]; ok {
				return response(201, []Session{}), nil
			}
			row := Session{Identity: identity, ID: meetingID, UserID: user, CreatedAt: "2026-10-18T00:00:00Z"}
			rows[key] = row
			return response(201, []Session{row}), nil
		}
		if r.URL.Query().Get("user_id") != "eq."+user {
			t.Error("missing owner filter")
		}
		result := []Session{}
		for _, row := range rows {
			if row.UserID == user && (r.URL.Query().Get("id") == "" || r.URL.Query().Get("id") == "eq."+row.ID) {
				result = append(result, row)
			}
		}
		return response(200, result), nil
	})}
	h := NewHandler(config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}, client)
	if w := request(h, "POST", "/api/meetings/resolve", `{"canonical_url":"https://meet.google.com/abc-defg-hij"}`, ""); w.Code != 401 {
		t.Fatalf("missing auth: %d", w.Code)
	}
	if w := request(h, "GET", "/api/meetings", "", "invalid"); w.Code != 401 {
		t.Fatalf("invalid auth: %d", w.Code)
	}
	for _, body := range []string{`{"canonical_url":"https://evil.test/abc"}`, `{"canonical_url":"https://meet.google.com/abc-defg-hij","user_id":"forged"}`, `{} {}`} {
		if w := request(h, "POST", "/api/meetings/resolve", body, "owner"); w.Code != 400 {
			t.Fatalf("invalid input: %d %s", w.Code, w.Body.String())
		}
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(h, "POST", "/api/meetings/resolve", `{"canonical_url":"https://meet.google.com/abc-defg-hij?authuser=2"}`, "owner")
			if w.Code != 200 || !strings.Contains(w.Body.String(), meetingID) || strings.Contains(w.Body.String(), "authuser") {
				t.Errorf("resolve: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if len(rows) != 1 {
		t.Fatalf("duplicate rows: %d", len(rows))
	}
	if w := request(h, "GET", "/api/meetings/"+meetingID, "", "other"); w.Code != 404 {
		t.Fatalf("other user access: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/api/meetings/"+meetingID, "", "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/api/meetings", "", "owner"); w.Code != 200 || !strings.Contains(w.Body.String(), meetingID) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}
