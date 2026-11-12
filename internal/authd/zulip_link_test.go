package authd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/authdb"
)

// testDB opens the authd database named by STOA_TEST_AUTHDB_URL and migrates
// it. Tests that need Postgres are skipped when the variable is unset.
func testDB(t *testing.T) *authdb.DB {
	t.Helper()
	dsn := os.Getenv("STOA_TEST_AUTHDB_URL")
	if dsn == "" {
		t.Skip("STOA_TEST_AUTHDB_URL not set; skipping Postgres-backed test")
	}
	ctx := context.Background()
	if err := authdb.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := authdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func testServer(t *testing.T) *Server {
	return &Server{cfg: Config{ExternalURL: "http://authd.test"}, db: testDB(t), mux: http.NewServeMux()}
}

// newUserToken creates a user (provider "service" for a service account) and
// returns its ID and a session token.
func newUserToken(t *testing.T, s *Server, provider string) (string, string) {
	t.Helper()
	ctx := context.Background()
	sub := uuid.New().String()
	userID, err := s.db.UpsertUser(ctx, provider, sub, sub+"@example.test", sub)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	raw, _, err := s.db.CreateSession(ctx, userID, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return userID, raw
}

func TestZulipLinkEndpoints_RequireServiceToken(t *testing.T) {
	s := testServer(t)
	_, userToken := newUserToken(t, s, "https://idp.test")
	_, svcToken := newUserToken(t, s, "service")
	email := uuid.New().String() + "@zulip.test"

	call := func(h http.HandlerFunc, method, target, body, token string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}

	endpoints := []struct {
		name         string
		h            http.HandlerFunc
		method, path string
		body         string
	}{
		{"start", s.handleZulipLinkStart, http.MethodPost, "/link/zulip/start", `{"zulip_email":"` + email + `"}`},
		{"resolve", s.handleZulipLinkResolve, http.MethodGet, "/link/zulip/resolve?email=" + url.QueryEscape(email), ""},
		{"status", s.handleZulipLinkStatus, http.MethodGet, "/link/zulip/status?email=" + url.QueryEscape(email), ""},
		{"unlink", s.handleZulipUnlink, http.MethodDelete, "/link/zulip/unlink?email=" + url.QueryEscape(email), ""},
	}
	for _, e := range endpoints {
		if got := call(e.h, e.method, e.path, e.body, userToken); got != http.StatusForbidden {
			t.Errorf("%s with a user token: got %d, want 403", e.name, got)
		}
	}
	// The bridge's service token still works.
	if got := call(s.handleZulipLinkStart, http.MethodPost, "/link/zulip/start", `{"zulip_email":"`+email+`"}`, svcToken); got != http.StatusOK {
		t.Errorf("start with service token: got %d, want 200", got)
	}
}

func TestCreateZulipLink_DoesNotOverwriteAnotherUsersLink(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	victim, _ := newUserToken(t, s, "https://idp.test")
	attacker, _ := newUserToken(t, s, "https://idp.test")
	email := uuid.New().String() + "@zulip.test"

	if err := s.db.CreateZulipLink(ctx, email, victim); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if err := s.db.CreateZulipLink(ctx, email, attacker); !errors.Is(err, authdb.ErrZulipAlreadyLinked) {
		t.Fatalf("relink to another user: err = %v, want ErrZulipAlreadyLinked", err)
	}
	if got, _ := s.db.ResolveZulipLink(ctx, email); got != victim {
		t.Fatalf("link now points at %q, want the original user", got)
	}
	if err := s.db.CreateZulipLink(ctx, email, victim); err != nil {
		t.Fatalf("relink to the same user: %v", err)
	}
}

func TestZulipLink_RequiresConfirmationFromTheSignedInBrowser(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	userID, _ := newUserToken(t, s, "https://idp.test")
	email := "<b>" + uuid.New().String() + "</b>@zulip.test"

	linkToken, err := s.db.CreateZulipLinkToken(ctx, email, time.Minute)
	if err != nil {
		t.Fatalf("create link token: %v", err)
	}

	// OIDC callback: shows a confirmation page, links nothing yet.
	rec := httptest.NewRecorder()
	s.startZulipLinkConfirmation(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback", nil), linkToken, userID, "me@example.test")
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: got %d", rec.Code)
	}
	page := rec.Body.String()
	if strings.Contains(page, "<b>") {
		t.Fatal("confirmation page does not escape the Zulip address")
	}
	if _, err := s.db.ResolveZulipLink(ctx, email); err == nil {
		t.Fatal("link was stored before the user confirmed it")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == zulipLinkConfirmCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.SameSite != http.SameSiteStrictMode || !cookie.HttpOnly {
		t.Fatalf("confirmation cookie missing or not Strict/HttpOnly: %+v", cookie)
	}

	confirm := func(withCookie bool) int {
		form := url.Values{"nonce": {cookie.Value}}
		req := httptest.NewRequest(http.MethodPost, "/link/zulip/confirm", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCookie {
			req.AddCookie(&http.Cookie{Name: zulipLinkConfirmCookie, Value: cookie.Value})
		}
		rec := httptest.NewRecorder()
		s.handleZulipLinkConfirm(rec, req)
		return rec.Code
	}

	// A cross-site form post (no cookie) is refused.
	if got := confirm(false); got != http.StatusBadRequest {
		t.Fatalf("confirm without cookie: got %d, want 400", got)
	}
	if _, err := s.db.ResolveZulipLink(ctx, email); err == nil {
		t.Fatal("link stored without the confirmation cookie")
	}

	if got := confirm(true); got != http.StatusOK {
		t.Fatalf("confirm: got %d, want 200", got)
	}
	if got, _ := s.db.ResolveZulipLink(ctx, email); got != userID {
		t.Fatalf("link points at %q, want %q", got, userID)
	}

	// The nonce is single-use, and is not usable as a link token.
	if _, err := s.db.PeekZulipLinkToken(ctx, cookie.Value); err == nil {
		t.Fatal("confirmation nonce accepted as a link token")
	}
}
