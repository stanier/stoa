package authd

import (
	"encoding/json"
	"net/http"
	"time"

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

	// Require a valid Bearer session (the bridge's service token).
	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.verifyBearerToken(r.Context(), raw); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if err := s.verifyBearerToken(r.Context(), raw); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if err := s.verifyBearerToken(r.Context(), raw); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
	if err := s.verifyBearerToken(r.Context(), raw); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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

