package agentd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/stanier/stoa/internal/authdb"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/oauth"
)

// deviceFlowEntry tracks an in-progress Device Authorization Grant.
type deviceFlowEntry struct {
	provider    *oauth.Provider
	deviceCode  string
	expiresAt   time.Time
	interval    time.Duration
	lastPollAt  time.Time
}

// setupAuth connects to Postgres and discovers all configured OAuth providers.
func (s *Server) setupAuth(cfg *config.Config) error {
	dsn := os.Getenv("AGENTD_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("auth is enabled but AGENTD_DATABASE_URL is not set")
	}

	db, err := authdb.Open(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect to auth database: %w", err)
	}
	s.authDB = db

	// Verify schema version before accepting traffic.
	if err := authdb.CheckSchemaVersion(context.Background(), dsn); err != nil {
		db.Close()
		s.authDB = nil
		return err
	}

	// Discover all configured providers.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, pcfg := range cfg.Auth.Providers {
		p, err := oauth.Discover(ctx, pcfg)
		if err != nil {
			// Log and skip — one misconfigured provider shouldn't block the others.
			logging.Warnf("agentd: auth: skipping provider %q: %v", pcfg.ID, err)
			continue
		}
		s.providers = append(s.providers, p)
		logging.Infof("agentd: auth: provider %q ready (%s)", p.ID, p.IssuerURL)
	}

	if len(s.providers) == 0 {
		return fmt.Errorf("auth is enabled but no providers could be initialised")
	}

	ttl := cfg.Auth.SessionTTLHours
	if ttl == 0 {
		ttl = 24
	}
	logging.Infof("agentd: auth enabled — %d provider(s), session TTL %dh", len(s.providers), ttl)
	return nil
}

// sessionTTL returns the configured session lifetime.
func (s *Server) sessionTTL() time.Duration {
	if s.cfg != nil && s.cfg.Auth.SessionTTLHours > 0 {
		return time.Duration(s.cfg.Auth.SessionTTLHours) * time.Hour
	}
	return 24 * time.Hour
}

// findProvider returns the provider with the given ID, or the first provider
// if id is empty and there is exactly one configured.
func (s *Server) findProvider(id string) *oauth.Provider {
	if id == "" && len(s.providers) == 1 {
		return s.providers[0]
	}
	for _, p := range s.providers {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// ── POST /auth/device ─────────────────────────────────────────────────────────

// handleDeviceStart initiates an OAuth2 Device Authorization Grant.
//
// Request body (JSON, optional when only one provider is configured):
//
//	{ "provider_id": "gitlab-ce" }
//
// Response:
//
//	{ "polling_token": "...", "user_code": "ABCD-1234",
//	  "verification_uri": "https://...", "expires_in": 300, "interval": 5 }
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authDB == nil {
		http.Error(w, "auth not enabled", http.StatusNotImplemented)
		return
	}

	var req struct {
		ProviderID string `json:"provider_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	p := s.findProvider(req.ProviderID)
	if p == nil {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	dc, err := p.StartDeviceFlow(r.Context())
	if err != nil {
		logging.Warnf("agentd: auth: device flow start error: %v", err)
		http.Error(w, "failed to start device flow", http.StatusBadGateway)
		return
	}

	// Generate a random polling token the TUI uses to identify this flow.
	pollingToken, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	entry := &deviceFlowEntry{
		provider:   p,
		deviceCode: dc.DeviceCode,
		expiresAt:  time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second),
		interval:   time.Duration(dc.Interval) * time.Second,
	}
	s.deviceFlows.Store(pollingToken, entry)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"polling_token":    pollingToken,
		"user_code":        dc.UserCode,
		"verification_uri": dc.VerificationURI,
		"expires_in":       dc.ExpiresIn,
		"interval":         dc.Interval,
	})
}

// ── GET /auth/device/poll ─────────────────────────────────────────────────────

// handleDevicePoll polls for the result of an in-progress device flow.
//
// Query param: token=<polling_token>
//
// Responses:
//
//	202 { "status": "pending" }          — still waiting for user
//	200 { "session_token": "..." }       — authorized; use as Bearer token
//	400 { "error": "expired" }           — device code expired
//	400 { "error": "denied" }            — user denied the request
func (s *Server) handleDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authDB == nil {
		http.Error(w, "auth not enabled", http.StatusNotImplemented)
		return
	}

	pollingToken := r.URL.Query().Get("token")
	if pollingToken == "" {
		http.Error(w, "missing token parameter", http.StatusBadRequest)
		return
	}

	val, ok := s.deviceFlows.Load(pollingToken)
	if !ok {
		http.Error(w, "unknown or expired polling token", http.StatusNotFound)
		return
	}
	entry := val.(*deviceFlowEntry)

	if time.Now().After(entry.expiresAt) {
		s.deviceFlows.Delete(pollingToken)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired"})
		return
	}

	// Enforce minimum polling interval.
	if !entry.lastPollAt.IsZero() && time.Since(entry.lastPollAt) < entry.interval {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		return
	}
	entry.lastPollAt = time.Now()

	tok, err := entry.provider.PollToken(r.Context(), entry.deviceCode)
	if err != nil {
		switch {
		case errors.Is(err, oauth.ErrAuthorizationPending):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		case errors.Is(err, oauth.ErrSlowDown):
			entry.interval += 5 * time.Second
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		case errors.Is(err, oauth.ErrAccessDenied):
			s.deviceFlows.Delete(pollingToken)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "denied"})
		case errors.Is(err, oauth.ErrDeviceCodeExpired):
			s.deviceFlows.Delete(pollingToken)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired"})
		default:
			logging.Warnf("agentd: auth: device poll error: %v", err)
			http.Error(w, "provider error", http.StatusBadGateway)
		}
		return
	}

	// Success — fetch userinfo, upsert user, create session.
	info, err := entry.provider.FetchUserinfo(r.Context(), tok.AccessToken)
	if err != nil {
		logging.Warnf("agentd: auth: userinfo error: %v", err)
		http.Error(w, "failed to fetch user info", http.StatusBadGateway)
		return
	}

	displayName := info.Name
	if displayName == "" {
		displayName = info.PreferredUsername
	}

	userID, err := s.authDB.UpsertUser(r.Context(), entry.provider.IssuerURL, info.Sub, info.Email, displayName)
	if err != nil {
		logging.Warnf("agentd: auth: upsert user error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rawToken, _, err := s.authDB.CreateSession(r.Context(), userID, s.sessionTTL())
	if err != nil {
		logging.Warnf("agentd: auth: create session error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.deviceFlows.Delete(pollingToken)

	logging.Infof("agentd: auth: user %s (%s) authenticated via %s", userID, info.Email, entry.provider.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"session_token": rawToken})
}

// ── POST /auth/logout ─────────────────────────────────────────────────────────

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authDB == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	raw := bearerToken(r)
	if raw != "" {
		_ = s.authDB.RevokeSession(r.Context(), raw)
	}
	w.WriteHeader(http.StatusNoContent)
}

// randomToken generates a cryptographically random base64url token.
func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
