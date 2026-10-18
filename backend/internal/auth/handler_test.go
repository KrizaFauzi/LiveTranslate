package auth

import (
	"context"
	"encoding/json"
	"io"
	"livetranslate/backend/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const testID = "11111111-2222-4333-8444-555555555555"
const sessionJSON = `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"token_type":"bearer","user":{"id":"` + testID + `","email":"a@example.com","app_metadata":{"private":"omit"}}}`

func authConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("SUPABASE_URL", "https://project.supabase.co")
	t.Setenv("SUPABASE_PUBLISHABLE_KEY", "sb_publishable_test")
	t.Setenv("SUPABASE_AUTH_REDIRECT_URL", "http://localhost:8989/auth/confirmed")
	return config.Load()
}
func upstream(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func authRequest(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthLoginDirectSupabase(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://project.supabase.co/auth/v1/token?grant_type=password" {
			t.Errorf("wrong target: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("apikey") != "sb_publishable_test" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Internal-Token") != "" || r.Header.Get("Cookie") != "" {
			t.Error("wrong authentication headers")
		}
		var input map[string]string
		json.NewDecoder(r.Body).Decode(&input)
		if input["email"] != "a@example.com" || input["password"] != " password1 " {
			t.Errorf("credentials changed: %#v", input)
		}
		return upstream(200, sessionJSON), nil
	})}
	w := authRequest(NewHandler(authConfig(t), client), "POST", "/api/auth/login?redirect_to=evil", `{"email":" a@example.com ","password":" password1 "}`, "")
	if w.Code != 200 || calls != 1 {
		t.Fatalf("%d: %s calls=%d", w.Code, w.Body.String(), calls)
	}
	var body struct {
		User    map[string]any `json:"user"`
		Session struct {
			Access  string `json:"access_token"`
			Expires int64  `json:"expires_at"`
		} `json:"session"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Session.Access != "access" || body.Session.Expires < time.Now().Unix()+3500 || body.User["id"] != testID || len(body.User) != 2 {
		t.Fatalf("wrong API contract: %s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cached session")
	}
}
func TestAuthRegisterConfirmationAndSession(t *testing.T) {
	for _, tc := range []struct {
		body string
		want int
	}{{`{"id":"` + testID + `","email":"a@example.com"}`, 202}, {sessionJSON, 201}} {
		t.Run(http.StatusText(tc.want), func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/auth/v1/signup" || r.URL.Query().Get("redirect_to") != "http://localhost:8989/auth/confirmed" {
					t.Errorf("signup target %s", r.URL)
				}
				var input struct {
					Data map[string]string `json:"data"`
				}
				json.NewDecoder(r.Body).Decode(&input)
				if input.Data["display_name"] != "Pengguna" {
					t.Error("missing signup metadata")
				}
				return upstream(200, tc.body), nil
			})}
			w := authRequest(NewHandler(authConfig(t), client), "POST", "/api/auth/register", `{"display_name":" Pengguna ","email":"a@example.com","password":"password1"}`, "")
			if w.Code != tc.want {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestAuthProfileUsesUserJWTAndOwnRow(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("apikey") != "sb_publishable_test" || r.Header.Get("Authorization") != "Bearer user-jwt" {
			t.Error("profile must use authenticated user's JWT")
		}
		if calls == 1 {
			if r.URL.Path != "/auth/v1/user" {
				t.Error(r.URL)
			}
			return upstream(200, `{"id":"`+testID+`","email":"a@example.com"}`), nil
		}
		if r.URL.Path != "/rest/v1/profiles" || r.URL.Query().Get("id") != "eq."+testID || r.URL.Query().Get("select") != "id,display_name,created_at,updated_at" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("wrong own-row query: %s", r.URL)
		}
		return upstream(200, `[{"id":"`+testID+`","display_name":"Pengguna","created_at":"2026-10-18T00:00:00Z","updated_at":"2026-10-18T00:00:00Z"}]`), nil
	})}
	w := authRequest(NewHandler(authConfig(t), client), "GET", "/api/auth/me?id=someone-else", "", "user-jwt")
	if w.Code != 200 || calls != 2 || !strings.Contains(w.Body.String(), `"display_name":"Pengguna"`) {
		t.Fatalf("%d: %s calls=%d", w.Code, w.Body.String(), calls)
	}
}
func TestAuthProfileErrorsIdentifyStage(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
		want       int
	}{
		{200, `[]`, "profile_missing", 503}, {404, `{"code":"PGRST205","message":"private SQL"}`, "profile_api_error", 502},
		{403, `{"message":"private SQL"}`, "profile_api_error", 502}, {503, `{"message":"private SQL"}`, "profile_api_error", 502},
		{200, `[{"id":"another-user","display_name":"private"}]`, "profile_response_invalid", 502},
	} {
		t.Run(tc.code+http.StatusText(tc.status), func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/auth/v1/user" {
					return upstream(200, `{"id":"`+testID+`"}`), nil
				}
				return upstream(tc.status, tc.body), nil
			})}
			w := authRequest(NewHandler(authConfig(t), client), "GET", "/api/auth/me", "", "user-jwt")
			if w.Code != tc.want || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestAuthRefreshAndLocalLogout(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/auth/v1/token":
			if r.URL.Query().Get("grant_type") != "refresh_token" {
				t.Error(r.URL)
			}
			var input map[string]string
			json.NewDecoder(r.Body).Decode(&input)
			if input["refresh_token"] != "old-refresh" {
				t.Error(input)
			}
			return upstream(200, sessionJSON), nil
		case "/auth/v1/logout":
			if r.URL.Query().Get("scope") != "local" || r.Header.Get("Authorization") != "Bearer user-jwt" {
				t.Error("wrong logout scope/token")
			}
			return upstream(204, ""), nil
		default:
			t.Fatalf("unexpected endpoint: %s", r.URL)
			return nil, nil
		}
	})}
	h := NewHandler(authConfig(t), client)
	if w := authRequest(h, "POST", "/api/auth/refresh", `{"refresh_token":"old-refresh"}`, ""); w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	if w := authRequest(h, "POST", "/api/auth/logout", "", "user-jwt"); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
}
func TestAuthRejectsInvalidRequestsWithoutUpstream(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached Supabase")
		return nil, nil
	})}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/auth/admin", "", 404}, {"GET", "/api/auth/login", "", 405}, {"GET", "/api/auth/me", "", 401},
		{"POST", "/api/auth/login", `{}`, 400}, {"POST", "/api/auth/login", `{"email":"bad","password":"password1"}`, 400},
		{"POST", "/api/auth/login", `{"email":"a@example.com","password":"password1","role":"admin"}`, 400},
		{"POST", "/api/auth/login", `{"email":"a@example.com","password":"password1"} {}`, 400},
		{"POST", "/api/auth/refresh", `{"refresh_token":" "}`, 400},
		{"POST", "/api/auth/register", `{"email":"a@example.com","password":"password1","display_name":" "}`, 400},
		{"POST", "/api/auth/login", strings.Repeat("x", 17000), 413},
	} {
		t.Run(tc.method+tc.path+tc.body[:min(len(tc.body), 20)], func(t *testing.T) {
			w := authRequest(NewHandler(authConfig(t), client), tc.method, tc.path, tc.body, "")
			if w.Code != tc.want {
				t.Fatalf("%d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	h := NewHandler(authConfig(t), client)
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}
func TestAuthProviderFailuresAndRateLimit(t *testing.T) {
	for _, tc := range []struct {
		status, want int
		code         string
	}{{400, 401, "unauthorized"}, {422, 401, "unauthorized"}, {429, 429, "rate_limit"}, {500, 502, "auth_api_error"}, {503, 502, "auth_api_error"}, {302, 502, "auth_api_error"}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				return upstream(tc.status, `{"message":"password leaked"}`), nil
			})}
			h := NewHandler(authConfig(t), client)
			for i := 0; i < 11; i++ {
				w := authRequest(h, "POST", "/api/auth/login", `{"email":"a@example.com","password":"password1"}`, "")
				want := tc.want
				if i == 10 {
					want = 429
				}
				if w.Code != want || strings.Contains(w.Body.String(), "password leaked") {
					t.Fatalf("attempt %d: %d: %s", i, w.Code, w.Body.String())
				}
				if i == 0 && !strings.Contains(w.Body.String(), tc.code) {
					t.Fatal(w.Body.String())
				}
			}
		})
	}
}
func TestAuthTimeout(t *testing.T) {
	client := &http.Client{Timeout: time.Millisecond, Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, context.DeadlineExceeded
	})}
	w := authRequest(NewHandler(authConfig(t), client), "GET", "/api/auth/me", "", "user-jwt")
	if w.Code != 504 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
}
func TestAuthMissingConfigIsActionable(t *testing.T) {
	w := authRequest(NewHandler(config.Config{}, nil), "GET", "/api/auth/me", "", "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "SUPABASE_URL") || !strings.Contains(w.Body.String(), "supabase_config") {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
}

func TestAuthLoginAcceptsReachableHTTPURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.RequestURI() != "/auth/v1/token?grant_type=password" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, sessionJSON)
	}))
	defer server.Close()
	c := authConfig(t)
	c.SupabaseURL = server.URL
	w := authRequest(NewHandler(c, nil), "POST", "/api/auth/login", `{"email":"a@example.com","password":"password1"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
}
