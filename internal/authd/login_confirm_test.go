package authd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// --- #8: each login is bound to the client that started it ---

// fakeIdP returns a provider whose token and userinfo endpoints accept any
// code and report the given user.
func fakeIdP(t *testing.T, email string) *provider {
	t.Helper()
	sub := uuid.New().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "token_type": "Bearer"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sub": sub, "email": email})
	})
	idp := httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	return &provider{
		id:                    "test",
		issuerURL:             idp.URL,
		clientID:              "authd",
		authorizationEndpoint: idp.URL + "/authorize",
		tokenEndpoint:         idp.URL + "/token",
		userinfoEndpoint:      idp.URL + "/userinfo",
	}
}

func loginTestServer(t *testing.T) *Server {
	s := testServer(t)
	s.providers = []*provider{fakeIdP(t, "victim@example.test")}
	s.allowedRedirectOrigins = []string{"http://webd.test"}
	s.fileCfg = &authdConfig{}
	return s
}

type loginStart struct {
	LoginID  string `json:"login_id"`
	URL      string `json:"url"`
	UserCode string `json:"user_code"`
}

func startLogin(t *testing.T, s *Server, postLoginRedirect string) (loginStart, string) {
	t.Helper()
	body := `{}`
	if postLoginRedirect != "" {
		body = `{"post_login_redirect":"` + postLoginRedirect + `"}`
	}
	rec := httptest.NewRecorder()
	s.handleLoginStart(rec, httptest.NewRequest(http.MethodPost, "/oauth/login", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("login start: %d %s", rec.Code, rec.Body)
	}
	var ls loginStart
	if err := json.NewDecoder(rec.Body).Decode(&ls); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ls.URL)
	if err != nil {
		t.Fatal(err)
	}
	return ls, u.Query().Get("state")
}

// idpCallback simulates the browser returning from the IdP.
func idpCallback(s *Server, state string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?code=c&state="+url.QueryEscape(state), nil))
	return rec
}

func poll(s *Server, loginID string) (int, map[string]string) {
	rec := httptest.NewRecorder()
	s.handleLoginPoll(rec, httptest.NewRequest(http.MethodGet, "/oauth/login/poll?id="+url.QueryEscape(loginID), nil))
	var out map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func exchange(s *Server, loginID, code string) (int, map[string]string) {
	b, _ := json.Marshal(map[string]string{"login_id": loginID, "code": code})
	rec := httptest.NewRecorder()
	s.handleLoginExchange(rec, httptest.NewRequest(http.MethodPost, "/oauth/login/exchange", strings.NewReader(string(b))))
	var out map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func TestWebLogin_RedirectsWithOneTimeCodeNotToken(t *testing.T) {
	s := loginTestServer(t)
	ls, state := startLogin(t, s, "http://webd.test/auth/callback")

	rec := idpCallback(s, state)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if q.Has("token") || q.Has("llm_key") {
		t.Fatalf("redirect still carries credentials: %s", loc)
	}
	if q.Get("login_id") != ls.LoginID || q.Get("code") == "" {
		t.Fatalf("redirect missing login_id/code: %s", loc)
	}

	// Whoever started the login (and so knows login_id) can't poll for the token.
	if code, out := poll(s, ls.LoginID); code == http.StatusOK || out["session_token"] != "" {
		t.Fatalf("web login was pollable: %d %v", code, out)
	}
	// Nor can they guess the code.
	if code, _ := exchange(s, ls.LoginID, "wrong"); code != http.StatusBadRequest {
		t.Fatalf("exchange with a wrong code: %d, want 400", code)
	}

	code, out := exchange(s, ls.LoginID, q.Get("code"))
	if code != http.StatusOK || out["session_token"] == "" {
		t.Fatalf("exchange: %d %v", code, out)
	}
	if _, err := s.db.ValidateSession(t.Context(), out["session_token"]); err != nil {
		t.Fatalf("exchanged token is not a valid session: %v", err)
	}
	// The code is single-use.
	if code, _ := exchange(s, ls.LoginID, q.Get("code")); code != http.StatusBadRequest {
		t.Fatalf("second exchange: %d, want 400", code)
	}
}

func TestTerminalLogin_RequiresConfirmationInTheBrowser(t *testing.T) {
	s := loginTestServer(t)
	ls, state := startLogin(t, s, "")
	if ls.UserCode == "" || ls.UserCode != loginUserCode(ls.LoginID) {
		t.Fatalf("user_code = %q", ls.UserCode)
	}

	rec := idpCallback(s, state)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), ls.UserCode) {
		t.Fatal("confirmation page does not show the terminal's code")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginConfirmCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.SameSite != http.SameSiteStrictMode || !cookie.HttpOnly {
		t.Fatalf("confirmation cookie missing or not Strict/HttpOnly: %+v", cookie)
	}

	// Signing in at the IdP is not enough: the poller still waits.
	if code, out := poll(s, ls.LoginID); code != http.StatusAccepted {
		t.Fatalf("poll before confirmation: %d %v, want 202", code, out)
	}

	confirm := func(withCookie bool) int {
		form := url.Values{"login_id": {ls.LoginID}, "nonce": {cookie.Value}, "action": {"confirm"}}
		req := httptest.NewRequest(http.MethodPost, "/oauth/login/confirm", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCookie {
			req.AddCookie(&http.Cookie{Name: loginConfirmCookie, Value: cookie.Value})
		}
		rec := httptest.NewRecorder()
		s.handleLoginConfirm(rec, req)
		return rec.Code
	}

	// A cross-site form post (no cookie) is refused.
	if got := confirm(false); got != http.StatusBadRequest {
		t.Fatalf("confirm without cookie: %d, want 400", got)
	}
	if code, _ := poll(s, ls.LoginID); code != http.StatusAccepted {
		t.Fatalf("poll after a refused confirmation: %d, want 202", code)
	}

	if got := confirm(true); got != http.StatusOK {
		t.Fatalf("confirm: %d, want 200", got)
	}
	code, out := poll(s, ls.LoginID)
	if code != http.StatusOK || out["session_token"] == "" {
		t.Fatalf("poll after confirmation: %d %v", code, out)
	}
	// The confirmation is single-use.
	if got := confirm(true); got == http.StatusOK {
		t.Fatal("confirmation accepted twice")
	}
}

func TestTerminalLogin_CancelFailsThePoll(t *testing.T) {
	s := loginTestServer(t)
	ls, state := startLogin(t, s, "")
	rec := idpCallback(s, state)
	var nonce string
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginConfirmCookie {
			nonce = c.Value
		}
	}
	form := url.Values{"login_id": {ls.LoginID}, "nonce": {nonce}, "action": {"cancel"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/login/confirm", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: loginConfirmCookie, Value: nonce})
	s.handleLoginConfirm(httptest.NewRecorder(), req)

	code, out := poll(s, ls.LoginID)
	if code != http.StatusBadRequest || out["session_token"] != "" {
		t.Fatalf("poll after cancel: %d %v, want 400 with no token", code, out)
	}
}
