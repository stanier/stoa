package webd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stanier/stoa/internal/authdclient"
)

// --- #8: the login callback only completes in the browser that started it ---

func fakeAuthd(t *testing.T, exchanges *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"login_id": "L1", "url": "http://idp.test/authorize"})
		case "/oauth/login/exchange":
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"session_token": "tok", "llm_key": "key"})
		case "/token/verify":
			_ = json.NewEncoder(w).Encode(map[string]string{"user_id": "u1"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleLogin_SetsLoginCookie(t *testing.T) {
	var n atomic.Int32
	authd := fakeAuthd(t, &n)
	s := &Server{cfg: &Config{AuthdURL: authd.URL, ExternalURL: "http://webd.test"}, httpClient: authd.Client()}

	rec := httptest.NewRecorder()
	s.handleLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == authdclient.LoginCookie && c.Value == "L1" && c.HttpOnly {
			return
		}
	}
	t.Fatal("login did not remember login_id on the browser")
}

func TestHandleCallback_RejectsLoginStartedElsewhere(t *testing.T) {
	var exchanges atomic.Int32
	authd := fakeAuthd(t, &exchanges)
	s := &Server{cfg: &Config{AuthdURL: authd.URL, ExternalURL: "http://webd.test"}, httpClient: authd.Client()}

	// A victim lands on the callback of a login the attacker started: their
	// browser has no (or a different) login cookie.
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?login_id=L1&code=C", nil)
	rec := httptest.NewRecorder()
	s.handleCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("callback without login cookie: %d, want 400", rec.Code)
	}
	if exchanges.Load() != 0 {
		t.Fatal("webd exchanged a code for a login this browser didn't start")
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("session cookie set for a foreign login")
		}
	}

	// The browser that started the login completes it.
	req = httptest.NewRequest(http.MethodGet, "/auth/callback?login_id=L1&code=C", nil)
	req.AddCookie(&http.Cookie{Name: authdclient.LoginCookie, Value: "L1"})
	rec = httptest.NewRecorder()
	s.handleCallback(rec, req)
	if rec.Code != http.StatusFound || exchanges.Load() != 1 {
		t.Fatalf("own callback: code=%d exchanges=%d", rec.Code, exchanges.Load())
	}
	var gotSession, gotKey bool
	for _, c := range rec.Result().Cookies() {
		gotSession = gotSession || (c.Name == sessionCookie && c.Value == "tok")
		gotKey = gotKey || (c.Name == llmKeyCookie && c.Value == "key")
	}
	if !gotSession || !gotKey {
		t.Fatal("session or LLM key cookie not set after a valid callback")
	}
}

func TestHandleCallback_IgnoresTokenInURL(t *testing.T) {
	var exchanges atomic.Int32
	authd := fakeAuthd(t, &exchanges)
	s := &Server{cfg: &Config{AuthdURL: authd.URL, ExternalURL: "http://webd.test"}, httpClient: authd.Client()}

	// The old ?token= form (login CSRF: plant the attacker's session) no longer works.
	rec := httptest.NewRecorder()
	s.handleCallback(rec, httptest.NewRequest(http.MethodGet, "/auth/callback?token=attacker-token", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("?token= callback: %d, want 400", rec.Code)
	}
}
