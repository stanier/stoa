package authd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/stanier/stoa/internal/authdb"
	"github.com/stanier/stoa/internal/logging"
)

// Every login started with POST /oauth/login is bound to the client that
// started it, so a login URL sent to someone else can't hand their session to
// the sender:
//
//   - Web flows (post_login_redirect set): the callback redirects back with a
//     one-time exchange code, never the token. The web app checks the login_id
//     against a cookie it set when the login started, then swaps the code for
//     the token with POST /oauth/login/exchange. Web flows can't be polled.
//   - Terminal flows: after the IdP login, the browser shows a short code that
//     also appears in the terminal and asks the user to confirm. The token
//     only reaches the poller once they do.

// loginConfirmCookie carries the confirmation nonce between the callback and
// POST /oauth/login/confirm. With SameSite=Strict, a page on another site
// can't submit the confirmation on the user's behalf.
const loginConfirmCookie = "stoa_login_confirm"

// loginUserCode is the short code shown both in the terminal (from
// POST /oauth/login) and on the browser's confirmation page.
func loginUserCode(loginID string) string {
	sum := sha256.Sum256([]byte("stoa-login-code:" + loginID))
	c := base32.StdEncoding.EncodeToString(sum[:5]) // 8 characters
	return c[:4] + "-" + c[4:]
}

// externalPath returns suffix under any path prefix AUTHD_EXTERNAL_URL carries.
func (s *Server) externalPath(suffix string) string {
	prefix := ""
	if u, err := url.Parse(s.cfg.ExternalURL); err == nil {
		prefix = strings.TrimRight(u.Path, "/")
	}
	return prefix + suffix
}

// issueSession creates a session for userID and provisions its LLM key
// (best-effort: login succeeds even if apikeyd is down).
func (s *Server) issueSession(ctx context.Context, userID string) (rawToken, llmKey string, err error) {
	rawToken, _, err = s.db.CreateSession(ctx, userID, s.sessionTTL())
	if err != nil {
		return "", "", err
	}
	if s.apikeydClient != nil {
		if pk, pkErr := s.apikeydClient.ProvisionUserKey(ctx, userID, s.apikeydAdminToken); pkErr != nil {
			logging.Warnf("authd: provision LLM key for user %s: %v", userID, pkErr)
		} else {
			llmKey = pk.Key
		}
	}
	return rawToken, llmKey, nil
}

// startTerminalLoginConfirmation is called from the OIDC callback of a
// terminal (polling) login. It records who signed in and asks them to confirm
// that the login is their own before any session is issued.
func (s *Server) startTerminalLoginConfirmation(w http.ResponseWriter, r *http.Request, loginID, userID, email string) {
	nonce, err := s.db.BeginLoginConfirmation(r.Context(), loginID, userID)
	if err != nil {
		logging.Warnf("authd: login confirmation: %v", err)
		s.renderCallbackError(w, "This login has expired or was already used. Start it again from your terminal.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     loginConfirmCookie,
		Value:    nonce,
		Path:     s.externalPath("/oauth/login/confirm"),
		MaxAge:   600,
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.ExternalURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, loginConfirmHTML,
		html.EscapeString(email),
		html.EscapeString(loginUserCode(loginID)),
		html.EscapeString(strings.TrimRight(s.cfg.ExternalURL, "/")+"/oauth/login/confirm"),
		html.EscapeString(loginID),
		html.EscapeString(nonce),
	)
}

// ── POST /oauth/login/confirm ────────────────────────────────────────────────
//
// Form fields: login_id, nonce, action ("confirm" or "cancel"). The nonce must
// also arrive as the SameSite=Strict cookie set by the callback.
func (s *Server) handleLoginConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	loginID := r.FormValue("login_id")
	nonce := r.FormValue("nonce")
	cookie, err := r.Cookie(loginConfirmCookie)
	if loginID == "" || nonce == "" || err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(nonce)) != 1 {
		http.Error(w, "invalid or missing confirmation", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: loginConfirmCookie, Value: "", Path: s.externalPath("/oauth/login/confirm"), MaxAge: -1})

	ctx := r.Context()
	userID, err := s.db.TakeLoginConfirmation(ctx, loginID, nonce)
	if err != nil {
		s.renderCallbackError(w, "This login has expired or was already used. Start it again from your terminal.")
		return
	}
	if r.FormValue("action") == "cancel" {
		_ = s.db.FailLoginFlow(ctx, loginID, "login cancelled in the browser")
		s.renderCallbackError(w, "Login cancelled. You may close this tab.")
		return
	}

	rawToken, llmKey, err := s.issueSession(ctx, userID)
	if err != nil {
		logging.Warnf("authd: create session error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := s.db.CompleteLoginFlow(ctx, loginID, rawToken, llmKey); err != nil {
		logging.Warnf("authd: complete login flow: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	email := ""
	if u, err := s.db.GetUser(ctx, userID); err == nil {
		email = u.Email
	}
	logging.Infof("authd: user %s confirmed terminal login", userID)
	s.renderCallbackSuccess(w, email)
}

// ── POST /oauth/login/exchange ───────────────────────────────────────────────
//
// Called by a web app (webd, apikeyd) after authd redirected the browser back
// with ?login_id=...&code=.... Swaps the one-time code for the session.
//
// Request body: { "login_id": "...", "code": "..." }
// Response:     { "session_token": "...", "llm_key": "..." }
func (s *Server) handleLoginExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
		Code    string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	token, llmKey, err := s.db.ExchangeLoginCode(r.Context(), req.LoginID, req.Code)
	if errors.Is(err, authdb.ErrNotFound) {
		http.Error(w, "invalid or expired login code", http.StatusBadRequest)
		return
	}
	if err != nil {
		logging.Warnf("authd: login exchange: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	resp := map[string]string{"session_token": token}
	if llmKey != "" {
		resp["llm_key"] = llmKey
	}
	_ = json.NewEncoder(w).Encode(resp)
}

const loginConfirmHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Confirm sign-in</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d27;border:1px solid #2d3148;border-radius:12px;padding:2.5rem;text-align:center;max-width:420px}
h1{font-size:1.2rem;margin-bottom:.75rem}p{color:#94a3b8;font-size:.9rem;line-height:1.5}
.email{color:#a5b4fc;font-family:monospace}.code{font-family:monospace;font-size:1.6rem;letter-spacing:.15em;color:#e2e8f0}
button{margin:1rem .3rem 0;background:#4f46e5;color:#fff;border:0;border-radius:8px;padding:.6rem 1.2rem;font-size:.95rem;cursor:pointer}
button.cancel{background:#334155}</style></head>
<body><div class="card"><h1>Sign in to a terminal?</h1>
<p>A terminal is asking to sign in as <span class="email">%s</span>. It should be showing this code:</p>
<p class="code">%s</p>
<p>Only confirm if you started this login yourself and your terminal shows the same code. If someone sent you this link, cancel.</p>
<form method="post" action="%[3]s"><input type="hidden" name="login_id" value="%[4]s"><input type="hidden" name="nonce" value="%[5]s">
<button type="submit" name="action" value="confirm">Confirm sign-in</button><button type="submit" name="action" value="cancel" class="cancel">Cancel</button></form>
</div></body></html>`
