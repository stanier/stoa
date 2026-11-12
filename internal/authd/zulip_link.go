package authd

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/authdb"
	"github.com/stanier/stoa/internal/logging"
)

const zulipLinkTTL = 10 * time.Minute

// ── POST /link/zulip/start ────────────────────────────────────────────────────
//
// Called by the zulip-bridge (with its service Bearer token) to start a Zulip
// account link flow for a given sender email.
//
// Request:  { "zulip_email": "user@example.com" }
// Response: { "url": "http://authd:8888/link/zulip?token=...", "expires_in": 600 }
func (s *Server) handleZulipLinkStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	// Require the bridge's service token: only the bridge knows (from Zulip)
	// which address actually sent the !auth command.
	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.requireServiceToken(w, r.Context(), raw) {
		return
	}

	var req struct {
		ZulipEmail string `json:"zulip_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ZulipEmail == "" {
		http.Error(w, "zulip_email is required", http.StatusBadRequest)
		return
	}

	token, err := s.db.CreateZulipLinkToken(r.Context(), req.ZulipEmail, zulipLinkTTL)
	if err != nil {
		logging.Errorf("authd: zulip link start: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	url := s.cfg.ExternalURL + "/link/zulip?token=" + token
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"url":        url,
		"expires_in": int(zulipLinkTTL.Seconds()),
	})
}

// ── GET /link/zulip ───────────────────────────────────────────────────────────
//
// Browser visits this URL (from Zulip). Validates the link token then starts
// a normal OIDC Authorization Code flow with the link token embedded so the
// callback can complete the association.
func (s *Server) handleZulipLinkBrowser(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ExternalURL == "" {
		http.Error(w, "AUTHD_EXTERNAL_URL not configured", http.StatusInternalServerError)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	// Peek at the entry without consuming it (will be consumed after callback).
	if _, err := s.db.PeekZulipLinkToken(r.Context(), token); err != nil {
		http.Error(w, "link token is invalid or has expired", http.StatusBadRequest)
		return
	}

	p := s.findProvider("")
	if p == nil {
		http.Error(w, "no providers configured", http.StatusServiceUnavailable)
		return
	}

	state, codeChallenge, err := s.db.BeginOAuthFlow(r.Context(), p.id, zulipLinkTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.db.AttachZulipLinkToken(r.Context(), state, token); err != nil {
		logging.Warnf("authd: zulip link browser: attach token: %v", err)
	}

	authURL := p.authorizationURL(s.redirectURI(), state, codeChallenge)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ── GET /link/zulip/resolve ───────────────────────────────────────────────────
//
// Called by the zulip-bridge to look up the authd user_id for a Zulip email.
// Returns 404 if the email has not been linked.
//
// Query param: email=<zulip_email>
// Response:    { "user_id": "...", "email": "..." }
func (s *Server) handleZulipLinkResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.requireServiceToken(w, r.Context(), raw) {
		return
	}

	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "email parameter required", http.StatusBadRequest)
		return
	}

	userID, err := s.db.ResolveZulipLink(r.Context(), email)
	if err != nil {
		http.Error(w, "not linked", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"user_id": userID,
		"email":   email,
	})
}

// ── GET /link/zulip/status ────────────────────────────────────────────────────
//
// Called by the zulip-bridge to check whether a Zulip email is linked to an
// authd account AND that account has at least one non-expired session token.
//
// Query param: email=<zulip_email>
// Response:    { "user_id": "...", "email": "...", "authorized": true/false }
// 404 if no link exists.
func (s *Server) handleZulipLinkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.requireServiceToken(w, r.Context(), raw) {
		return
	}

	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "email parameter required", http.StatusBadRequest)
		return
	}

	userID, err := s.db.ResolveZulipLink(r.Context(), email)
	if err != nil {
		http.Error(w, "not linked", http.StatusNotFound)
		return
	}

	// A linked account is considered authorized. Active web sessions are not
	// required — the OAuth flow that established the link is sufficient proof
	// of identity. Revocation is handled by unlinking (!unlink or auth-admin).
	type roleInfo struct {
		Name         string   `json:"name"`
		Persona      string   `json:"persona"`
		AllowedTools []string `json:"allowed_tools"`
	}
	resp := map[string]interface{}{
		"user_id":    userID,
		"email":      email,
		"authorized": true,
	}
	if ur, err := s.db.GetUserRole(r.Context(), userID); err == nil {
		resp["role"] = roleInfo{
			Name:         ur.RoleName,
			Persona:      ur.Persona,
			AllowedTools: ur.AllowedTools,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ── DELETE /link/zulip/unlink ─────────────────────────────────────────────────
//
// Removes the Zulip account link for the given email. Called by the bridge.
//
// Query param: email=<zulip_email>
func (s *Server) handleZulipUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.requireServiceToken(w, r.Context(), raw) {
		return
	}
	email := r.URL.Query().Get("email")
	if email == "" {
		http.Error(w, "email parameter required", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteZulipLink(r.Context(), email); err != nil {
		logging.Warnf("authd: zulip unlink %q: %v", email, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	logging.Infof("authd: unlinked Zulip %q", email)
	w.WriteHeader(http.StatusNoContent)
}


// requireServiceToken checks that raw is a valid session token belonging to a
// service account (a bridge). It writes the error response and returns false
// otherwise. The Zulip link endpoints are bridge-only: a user token must not
// be able to start, resolve or remove links for arbitrary Zulip addresses.
func (s *Server) requireServiceToken(w http.ResponseWriter, ctx context.Context, raw string) bool {
	isService, err := s.tokenIsService(ctx, raw)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !isService {
		http.Error(w, "forbidden: service token required", http.StatusForbidden)
		return false
	}
	return true
}

// tokenIsService validates raw and reports whether it belongs to a service account.
func (s *Server) tokenIsService(ctx context.Context, raw string) (bool, error) {
	sess, err := s.db.ValidateSession(ctx, raw)
	if err != nil {
		return false, err
	}
	user, err := s.db.GetUser(ctx, sess.UserID)
	if err != nil {
		return false, err
	}
	return user.ProviderID == "service", nil
}

// zulipLinkConfirmCookie carries the pending-link nonce between the OIDC
// callback and POST /link/zulip/confirm. With SameSite=Strict, a page on
// another site cannot submit the confirmation on the user's behalf.
const zulipLinkConfirmCookie = "stoa_zulip_link"

// startZulipLinkConfirmation is called from the OIDC callback of a Zulip link
// flow. Instead of linking straight away, it records a pending link for the
// signed-in user and asks them to confirm it, so a link URL someone else
// started cannot silently bind their Zulip address to this account.
func (s *Server) startZulipLinkConfirmation(w http.ResponseWriter, r *http.Request, linkToken, userID, userEmail string) {
	ctx := r.Context()
	linkEntry, err := s.db.ConsumeZulipLinkToken(ctx, linkToken)
	if err != nil {
		logging.Warnf("authd: zulip link callback: consume token: %v", err)
		s.renderCallbackError(w, "This Zulip link has expired or was already used. Run !auth in Zulip again.")
		return
	}
	nonce, err := s.db.CreateZulipLinkConfirmation(ctx, linkEntry.ZulipEmail, userID, zulipLinkTTL)
	if err != nil {
		logging.Warnf("authd: zulip link callback: create confirmation: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     zulipLinkConfirmCookie,
		Value:    nonce,
		Path:     s.zulipConfirmCookiePath(),
		MaxAge:   int(zulipLinkTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.ExternalURL, "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, zulipLinkConfirmHTML,
		html.EscapeString(linkEntry.ZulipEmail),
		html.EscapeString(userEmail),
		html.EscapeString(strings.TrimRight(s.cfg.ExternalURL, "/")+"/link/zulip/confirm"),
		html.EscapeString(nonce),
	)
}

// zulipConfirmCookiePath scopes the confirmation cookie to the confirm
// endpoint, under any path prefix AUTHD_EXTERNAL_URL carries.
func (s *Server) zulipConfirmCookiePath() string {
	prefix := ""
	if u, err := url.Parse(s.cfg.ExternalURL); err == nil {
		prefix = strings.TrimRight(u.Path, "/")
	}
	return prefix + "/link/zulip/confirm"
}

// ── POST /link/zulip/confirm ──────────────────────────────────────────────────
//
// Completes a Zulip link after the signed-in user confirmed it. The nonce must
// arrive both as the form field and as the SameSite=Strict cookie set by the
// callback.
func (s *Server) handleZulipLinkConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	nonce := r.FormValue("nonce")
	cookie, err := r.Cookie(zulipLinkConfirmCookie)
	if nonce == "" || err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(nonce)) != 1 {
		http.Error(w, "invalid or missing confirmation", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: zulipLinkConfirmCookie, Value: "", Path: s.zulipConfirmCookiePath(), MaxAge: -1})

	entry, err := s.db.ConsumeZulipLinkConfirmation(r.Context(), nonce)
	if err != nil {
		s.renderCallbackError(w, "This Zulip link has expired or was already used. Run !auth in Zulip again.")
		return
	}
	if err := s.db.CreateZulipLink(r.Context(), entry.ZulipEmail, entry.UserID); err != nil {
		if errors.Is(err, authdb.ErrZulipAlreadyLinked) {
			s.renderCallbackError(w, "This Zulip address is already linked to another account. Run !unlink in Zulip first, then !auth again.")
			return
		}
		logging.Warnf("authd: zulip link confirm: store link: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	logging.Infof("authd: linked Zulip %q → user %s", entry.ZulipEmail, entry.UserID)
	s.renderZulipLinkedPage(w, entry.ZulipEmail)
}

const zulipLinkConfirmHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Link Zulip account?</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d27;border:1px solid #2d3148;border-radius:12px;padding:2.5rem;text-align:center;max-width:420px}
h1{font-size:1.2rem;margin-bottom:.75rem}p{color:#94a3b8;font-size:.9rem;line-height:1.5}
.email{color:#a5b4fc;font-family:monospace}
button{margin-top:1rem;background:#4f46e5;color:#fff;border:0;border-radius:8px;padding:.6rem 1.2rem;font-size:.95rem;cursor:pointer}</style></head>
<body><div class="card"><h1>Link this Zulip account?</h1>
<p>Messages from Zulip address <span class="email">%s</span> will run as your account <span class="email">%s</span>, with its tools and data.</p>
<p>Only continue if you sent <code>!auth</code> from that Zulip account yourself. If someone sent you this link, close this tab.</p>
<form method="post" action="%s"><input type="hidden" name="nonce" value="%s"><button type="submit">Link account</button></form>
</div></body></html>`
