package authdclient

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Browser login helpers shared by the web apps (webd, apikeyd).
//
// The app starts a login with authd's POST /oauth/login and remembers the
// returned login_id in a cookie on the browser that started it. authd sends
// the browser back to /auth/callback?login_id=...&code=...; the app checks the
// login_id against the cookie, then swaps the one-time code for the session
// token with POST /oauth/login/exchange. A login link someone else started
// fails the cookie check, and no token ever appears in a URL.

// LoginCookie holds the login_id of the browser login in progress.
const LoginCookie = "__login_id"

// ErrLoginMismatch means the callback's login_id isn't the one this browser started.
var ErrLoginMismatch = errors.New("authdclient: login was not started in this browser")

// SetLoginCookie remembers loginID on the browser that is starting a login.
func SetLoginCookie(w http.ResponseWriter, loginID string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     LoginCookie,
		Value:    loginID,
		Path:     "/auth/callback",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   secure,
		// Lax, not Strict: the callback is a top-level redirect from the IdP.
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearLoginCookie removes the login cookie once the callback is handled.
func ClearLoginCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: LoginCookie, Value: "", Path: "/auth/callback", MaxAge: -1})
}

// CheckLoginCallback returns the login_id and code of a callback request,
// after checking that this browser started that login.
func CheckLoginCallback(r *http.Request) (loginID, code string, err error) {
	loginID = r.URL.Query().Get("login_id")
	code = r.URL.Query().Get("code")
	if loginID == "" || code == "" {
		return "", "", errors.New("authdclient: missing login_id or code")
	}
	c, cerr := r.Cookie(LoginCookie)
	if cerr != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(loginID)) != 1 {
		return "", "", ErrLoginMismatch
	}
	return loginID, code, nil
}

// ExchangeLoginCode swaps a browser login's one-time code for its session
// token and LLM key at authd.
func ExchangeLoginCode(ctx context.Context, hc *http.Client, authdURL, loginID, code string) (sessionToken, llmKey string, err error) {
	body, _ := json.Marshal(map[string]string{"login_id": loginID, "code": code})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(authdURL, "/")+"/oauth/login/exchange", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("authdclient: build exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("authdclient: exchange request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", fmt.Errorf("authdclient: exchange: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		SessionToken string `json:"session_token"`
		LLMKey       string `json:"llm_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.SessionToken == "" {
		return "", "", fmt.Errorf("authdclient: exchange: bad response")
	}
	return out.SessionToken, out.LLMKey, nil
}
