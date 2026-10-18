package httpapi

import (
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
func TestAuthProxy(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("X-Internal-Token") != "server-secret" {
			t.Error("wrong internal token")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("untrusted headers forwarded")
		}
		if r.URL.String() != "http://auth:3001/api/auth/login" {
			t.Error(r.URL.String())
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"session":{"access_token":"a"}}`))}, nil
	})}
	h := NewAuthGateway(config.Config{AuthServiceURL: "http://auth:3001", AuthInternalToken: "server-secret"}, client)
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"email":"a@example.com","password":"password1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "attacker")
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-Forwarded-For", "evil")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || calls != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d calls %d headers %v", w.Code, calls, w.Header())
	}
}
func TestAuthGatewayRejects(t *testing.T) {
	h := NewAuthGateway(config.Config{AuthServiceURL: "http://auth:3001", AuthInternalToken: "secret"}, nil)
	for _, tc := range []struct {
		method, path, body, content string
		want                        int
	}{
		{"POST", "/api/auth/admin", "", "", 404}, {"GET", "/api/auth/login", "", "", 405},
		{"POST", "/api/auth/login", "{}", "text/plain", 415}, {"POST", "/api/auth/login", strings.Repeat("a", 17000), "application/json", 413},
	} {
		t.Run(tc.path+tc.content, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
	w := httptest.NewRecorder()
	NewAuthGateway(config.Config{}, nil).ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/me", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestAuthRateLimitAndProviderFailure(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("database password leaked"))}, nil
	})}
	h := NewAuthGateway(config.Config{AuthServiceURL: "http://auth:3001", AuthInternalToken: "secret"}, client)
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 502
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d got %d want %d", i, w.Code, want)
		}
		if strings.Contains(w.Body.String(), "password leaked") {
			t.Fatal("leak")
		}
	}
}
func TestAuthTimeout(t *testing.T) {
	client := &http.Client{Timeout: time.Millisecond, Transport: transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	h := NewAuthGateway(config.Config{AuthServiceURL: "http://auth:3001", AuthInternalToken: "secret"}, client)
	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 504 {
		t.Fatal(w.Code)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetReadDeadline(value time.Time) error { w.deadline = value; return nil }
func TestAuthBodyHasReadDeadline(t *testing.T) {
	h := NewAuthGateway(config.Config{AuthServiceURL: "http://auth:3001", AuthInternalToken: "secret"}, nil)
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(strings.Repeat("a", 17000)))
	r.Header.Set("Content-Type", "application/json")
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, r)
	if w.deadline.IsZero() || time.Until(w.deadline) > 10*time.Second {
		t.Fatal("body must have a bounded read deadline")
	}
}
