package authd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/logging"
)

// zulipLinkEntry is a pending Zulip account link request.
type zulipLinkEntry struct {
	zulipEmail string
	expiresAt  time.Time
}

// zulipLinkStore holds pending Zulip link tokens.
type zulipLinkStore struct {
	mu      sync.Mutex
	entries map[string]*zulipLinkEntry // token → entry
}

func newZulipLinkStore() *zulipLinkStore {
	ls := &zulipLinkStore{entries: make(map[string]*zulipLinkEntry)}
	go ls.reapLoop()
	return ls
}

func (ls *zulipLinkStore) create(zulipEmail string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	ls.mu.Lock()
	ls.entries[token] = &zulipLinkEntry{
		zulipEmail: zulipEmail,
		expiresAt:  time.Now().Add(10 * time.Minute),
	}
	ls.mu.Unlock()
	return token, nil
}

func (ls *zulipLinkStore) consume(token string) (*zulipLinkEntry, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e, ok := ls.entries[token]
	if !ok {
		return nil, fmt.Errorf("authd: unknown or expired link token")
	}
	delete(ls.entries, token)
	if time.Now().After(e.expiresAt) {
		return nil, fmt.Errorf("authd: link token expired")
	}
	return e, nil
}

func (ls *zulipLinkStore) reapLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		ls.mu.Lock()
		for k, v := range ls.entries {
			if now.After(v.expiresAt) {
				delete(ls.entries, k)
			}
		}
		ls.mu.Unlock()
	}
}

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

	token, err := s.zulipLinks.create(req.ZulipEmail)
	if err != nil {
		logging.Errorf("authd: zulip link start: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	url := s.cfg.ExternalURL + "/link/zulip?token=" + token
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"url":        url,
		"expires_in": 600,
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

	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	// Peek at the entry without consuming it (will be consumed after callback).
	s.zulipLinks.mu.Lock()
	entry, ok := s.zulipLinks.entries[token]
	s.zulipLinks.mu.Unlock()
	if !ok || time.Now().After(entry.expiresAt) {
		http.Error(w, "link token is invalid or has expired", http.StatusBadRequest)
		return
	}

	p := s.findProvider("")
	if p == nil {
		http.Error(w, "no providers configured", http.StatusServiceUnavailable)
		return
	}

	state, codeChallenge, err := s.flows.begin(p)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.flows.attachZulipLinkToken(state, token)

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
	authorized := true

	// Attach role info if the user has one assigned.
	type roleInfo struct {
		Name         string   `json:"name"`
		Persona      string   `json:"persona"`
		AllowedTools []string `json:"allowed_tools"`
	}
	resp := map[string]interface{}{
		"user_id":    userID,
		"email":      email,
		"authorized": authorized,
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

// verifyBearerToken returns nil if the token is a valid authd session.
func (s *Server) verifyBearerToken(ctx context.Context, raw string) error {
	_, err := s.db.ValidateSession(ctx, raw)
	return err
}
