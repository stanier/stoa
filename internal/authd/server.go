// Package authd implements the authentication daemon — an OAuth2 identity
// broker that handles upstream provider flows and issues internal session tokens
// to all stoa services (agentd, bridges, TUI).
package authd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/apikeydclient"
	"github.com/stanier/stoa/internal/authdb"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/ratelimit"
)

// Config holds startup configuration passed in from main.
type Config struct {
	// ExternalURL is the public base URL authd is reachable at.
	// Used to construct the OAuth2 redirect URI registered with upstream providers.
	// Dev example:  http://localhost:8888
	// Prod example: https://auth.example.com
	ExternalURL string

	// Port is the local listen port (informational only; binding is done by main).
	Port string
}

// Server is the authd HTTP server.
type Server struct {
	cfg                    Config
	fileCfg                *authdConfig
	providers              []*provider
	db                     *authdb.DB
	mux                    *http.ServeMux
	adminToken             string
	allowedRedirectOrigins []string
	verifyLimiter          *ratelimit.Limiter // rate-limits /token/verify by remote IP

	// apikeydClient is set when AUTHD_APIKEYD_URL is configured.
	// Used to provision per-user stoa-infgw_* keys at login time (best-effort).
	apikeydClient     *apikeydclient.Client
	apikeydAdminToken string
}

// NewServer constructs, configures, and wires up a Server.
func NewServer(cfg Config) (*Server, error) {
	fileCfg, err := loadConfig()
	if err != nil {
		return nil, err
	}

	allowedOrigins := parseAllowedOrigins(os.Getenv("AUTHD_ALLOWED_REDIRECT_ORIGINS"))

	// AUTHD_VERIFY_RATE_LIMIT sets the max /token/verify calls per IP per minute.
	// Default: 60. Set to 0 to disable.
	verifyRateLimit := 60
	if v := os.Getenv("AUTHD_VERIFY_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			verifyRateLimit = n
		}
	}

	s := &Server{
		cfg:                    cfg,
		fileCfg:                fileCfg,
		mux:                    http.NewServeMux(),
		adminToken:             os.Getenv("AUTHD_ADMIN_TOKEN"),
		allowedRedirectOrigins: allowedOrigins,
		verifyLimiter:          ratelimit.New(time.Minute, verifyRateLimit),
		apikeydAdminToken:      os.Getenv("AUTHD_APIKEYD_ADMIN_TOKEN"),
	}

	// Wire up the apikeyd client for per-user LLM key provisioning at login.
	// Best-effort: if apikeyd is unavailable, login succeeds without a key.
	if apikeydURL := os.Getenv("AUTHD_APIKEYD_URL"); apikeydURL != "" && s.apikeydAdminToken != "" {
		s.apikeydClient = apikeydclient.New(apikeydURL)
		logging.Infof("authd: per-user LLM key provisioning enabled via %s", apikeydURL)
	}

	// Connect to Postgres if configured.
	if dsn := os.Getenv("AUTHD_DATABASE_URL"); dsn != "" {
		db, err := authdb.Open(context.Background(), dsn)
		if err != nil {
			return nil, fmt.Errorf("authd: connect to database: %w", err)
		}
		if err := authdb.CheckSchemaVersion(context.Background(), dsn); err != nil {
			db.Close()
			return nil, err
		}
		s.db = db
		logging.Infof("authd: connected to database")
	} else {
		logging.Warn("authd: AUTHD_DATABASE_URL not set — session persistence disabled")
	}

	// Discover all configured providers.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, pcfg := range fileCfg.Providers {
		p, err := discoverProvider(ctx, pcfg)
		if err != nil {
			logging.Warnf("authd: skipping provider %q: %v", pcfg.ID, err)
			continue
		}
		s.providers = append(s.providers, p)
		logging.Infof("authd: provider %q ready (%s)", p.id, p.issuerURL)
	}

	s.registerRoutes()
	return s, nil
}

// Router returns the HTTP handler for the server.
func (s *Server) Router() http.Handler {
	return s.mux
}

// redirectURI constructs the absolute OAuth2 callback URL.
func (s *Server) redirectURI() string {
	return fmt.Sprintf("%s/oauth/callback", strings.TrimRight(s.cfg.ExternalURL, "/"))
}

// sessionTTL returns the configured session lifetime.
func (s *Server) sessionTTL() time.Duration {
	if s.fileCfg.SessionTTL > 0 {
		return s.fileCfg.SessionTTL
	}
	return 24 * time.Hour
}

// serviceTokenTTL returns the configured service token lifetime.
func (s *Server) serviceTokenTTL() time.Duration {
	if s.fileCfg.ServiceTokenTTL > 0 {
		return s.fileCfg.ServiceTokenTTL
	}
	return 8760 * time.Hour
}

// findProvider returns the provider with the given ID, or the sole provider
// when id is empty and there is exactly one configured.
func (s *Server) findProvider(id string) *provider {
	if id == "" && len(s.providers) == 1 {
		return s.providers[0]
	}
	for _, p := range s.providers {
		if p.id == id {
			return p
		}
	}
	return nil
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/oauth/login", s.handleLoginStart)
	s.mux.HandleFunc("/oauth/login/poll", s.handleLoginPoll)
	s.mux.HandleFunc("/oauth/start", s.handleOAuthStart)
	s.mux.HandleFunc("/oauth/callback", s.handleOAuthCallback)
	s.mux.HandleFunc("/token/verify", s.handleTokenVerify)
	s.mux.HandleFunc("/token/service", s.handleTokenService)
	s.mux.HandleFunc("/.well-known/jwks.json", s.handleJWKS)
	// Zulip account linking
	s.mux.HandleFunc("/link/zulip/start", s.handleZulipLinkStart)
	s.mux.HandleFunc("/link/zulip/resolve", s.handleZulipLinkResolve)
	s.mux.HandleFunc("/link/zulip/status", s.handleZulipLinkStatus)
	s.mux.HandleFunc("/link/zulip/unlink", s.handleZulipUnlink)
	s.mux.HandleFunc("/link/zulip", s.handleZulipLinkBrowser)
	// Account overview (used by webd /api/account)
	s.mux.HandleFunc("/account/me", s.handleAccountMe)
	// MCP preferences (used by webd /api/mcp-prefs)
	s.mux.HandleFunc("/account/mcp-prefs", s.handleGetMCPPrefs)
	s.mux.HandleFunc("/account/mcp-prefs/servers/", s.handleSetMCPServerPref)
	s.mux.HandleFunc("/account/mcp-prefs/tools/", s.handleSetMCPToolPref)
}

// ── GET /healthz ──────────────────────────────────────────────────────────────

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// ── POST /oauth/login ─────────────────────────────────────────────────────────
//
// CLI entry point for the browser-based login flow. The TUI calls this to get
// a login_id and auth URL, opens the browser, then polls /oauth/login/poll.
//
// Request body (JSON, optional when only one provider is configured):
//
//	{ "provider_id": "local" }
//
// Response:
//
//	{ "login_id": "...", "url": "https://...", "expires_in": 300 }
func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.ExternalURL == "" {
		http.Error(w, "AUTHD_EXTERNAL_URL is not configured", http.StatusInternalServerError)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ProviderID        string `json:"provider_id"`
		PostLoginRedirect string `json:"post_login_redirect"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Validate post_login_redirect against the configured origin allowlist.
	if req.PostLoginRedirect != "" && !s.isAllowedRedirect(req.PostLoginRedirect) {
		http.Error(w, "post_login_redirect origin not in allowlist", http.StatusBadRequest)
		return
	}

	p := s.findProvider(req.ProviderID)
	if p == nil {
		if req.ProviderID == "" {
			http.Error(w, "no providers configured", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "unknown provider", http.StatusBadRequest)
		}
		return
	}

	const loginTTL = 5 * time.Minute
	ctx := r.Context()

	state, codeChallenge, err := s.db.BeginOAuthFlow(ctx, p.id, 10*time.Minute)
	if err != nil {
		logging.Errorf("authd: login start: begin flow: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authURL := p.authorizationURL(s.redirectURI(), state, codeChallenge)

	loginID, err := s.db.CreateLoginFlow(ctx, p.id, authURL, req.PostLoginRedirect, loginTTL)
	if err != nil {
		logging.Errorf("authd: login start: create login flow: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.db.UpdateOAuthFlowLoginID(ctx, state, loginID); err != nil {
		logging.Warnf("authd: login start: attach login id: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"login_id":   loginID,
		"url":        authURL,
		"expires_in": int(loginTTL.Seconds()),
	})
}

// ── GET /oauth/login/poll ─────────────────────────────────────────────────────
//
// CLI polls this until the browser flow completes.
//
// Query param: id=<login_id>
//
// Responses:
//
//	202 { "status": "pending" }              — browser flow not yet complete
//	200 { "session_token": "..." }           — success; use as Bearer token
//	400 { "error": "..." }                   — flow failed or expired
func (s *Server) handleLoginPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	loginID := r.URL.Query().Get("id")
	if loginID == "" {
		http.Error(w, "missing id parameter", http.StatusBadRequest)
		return
	}

	sessionToken, llmKey, errMsg, done, err := s.db.ConsumeLoginFlow(r.Context(), loginID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !done {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		return
	}
	if errMsg != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": errMsg})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]string{"session_token": sessionToken}
	if llmKey != "" {
		resp["llm_key"] = llmKey
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// ── GET /oauth/start ─────────────────────────────────────────────────────────
//
// Query params:
//
//	provider_id=<id>   (optional when only one provider is configured)
//
// Redirects the browser to the upstream provider's authorization URL.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.ExternalURL == "" {
		http.Error(w, "AUTHD_EXTERNAL_URL is not configured", http.StatusInternalServerError)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	providerID := r.URL.Query().Get("provider_id")
	p := s.findProvider(providerID)
	if p == nil {
		if providerID == "" {
			http.Error(w, "no providers configured", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "unknown provider", http.StatusBadRequest)
		}
		return
	}

	state, codeChallenge, err := s.db.BeginOAuthFlow(r.Context(), p.id, 10*time.Minute)
	if err != nil {
		logging.Errorf("authd: begin flow: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	authURL := p.authorizationURL(s.redirectURI(), state, codeChallenge)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ── GET /oauth/callback ───────────────────────────────────────────────────────
//
// Upstream providers redirect here after the user approves (or denies) access.
// Query params: code=<authorization_code>&state=<state>
//
// On success: exchanges the code, fetches userinfo, upserts user, creates session.
// Responds with JSON: { "session_token": "..." }
//
// In a production UI you would redirect to a frontend page that reads the token
// from a query param or cookie; for now we return JSON suitable for CLI/API use.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	// Check for upstream error response.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		logging.Warnf("authd: callback error from provider: %s — %s", errParam, desc)
		http.Error(w, fmt.Sprintf("provider error: %s", errParam), http.StatusBadRequest)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "missing state or code parameter", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	flow, err := s.db.ConsumeOAuthFlow(ctx, state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	p := s.findProvider(flow.ProviderID)
	if p == nil {
		http.Error(w, "provider no longer configured", http.StatusInternalServerError)
		return
	}

	tok, err := p.exchangeCode(ctx, code, s.redirectURI(), flow.CodeVerifier)
	if err != nil {
		logging.Warnf("authd: token exchange error: %v", err)
		if flow.LoginID != "" {
			_ = s.db.FailLoginFlow(ctx, flow.LoginID, "token exchange failed")
			s.renderCallbackError(w, "Authentication failed. You may close this tab.")
		} else {
			http.Error(w, "token exchange failed", http.StatusBadGateway)
		}
		return
	}

	info, err := p.fetchUserinfo(ctx, tok.AccessToken)
	if err != nil {
		logging.Warnf("authd: userinfo error: %v", err)
		if flow.LoginID != "" {
			_ = s.db.FailLoginFlow(ctx, flow.LoginID, "failed to fetch user info")
			s.renderCallbackError(w, "Authentication failed. You may close this tab.")
		} else {
			http.Error(w, "failed to fetch user info", http.StatusBadGateway)
		}
		return
	}

	displayName := info.Name
	if displayName == "" {
		displayName = info.PreferredUsername
	}

	userID, err := s.db.UpsertUser(ctx, p.issuerURL, info.Sub, info.Email, displayName)
	if err != nil {
		logging.Warnf("authd: upsert user error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rawToken, _, err := s.db.CreateSession(ctx, userID, s.sessionTTL())
	if err != nil {
		logging.Warnf("authd: create session error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Provision the per-user stoa-infgw_* LLM key (best-effort — login succeeds even if apikeyd is down).
	var llmKey string
	if s.apikeydClient != nil {
		if pk, pkErr := s.apikeydClient.ProvisionUserKey(ctx, userID, s.apikeydAdminToken); pkErr != nil {
			logging.Warnf("authd: provision LLM key for user %s: %v", userID, pkErr)
		} else {
			llmKey = pk.Key
		}
	}

	// Complete the polling login flow and get the BFF redirect URL in one query.
	var postLoginRedirect string
	if flow.LoginID != "" {
		postLoginRedirect, err = s.db.CompleteLoginFlow(ctx, flow.LoginID, rawToken, llmKey)
		if err != nil {
			logging.Warnf("authd: complete login flow: %v", err)
		}
	}

	// If this was a Zulip account link flow, store the email → user_id mapping.
	if flow.ZulipLinkToken != "" {
		linkEntry, err := s.db.ConsumeZulipLinkToken(ctx, flow.ZulipLinkToken)
		if err != nil {
			logging.Warnf("authd: zulip link callback: consume token: %v", err)
		} else if dbErr := s.db.CreateZulipLink(ctx, linkEntry.ZulipEmail, userID); dbErr != nil {
			logging.Warnf("authd: zulip link callback: store link: %v", dbErr)
		} else {
			logging.Infof("authd: linked Zulip %q → user %s", linkEntry.ZulipEmail, userID)
			s.renderZulipLinkedPage(w, linkEntry.ZulipEmail)
			return
		}
	}

	logging.Infof("authd: user %s (%s) authenticated via %s", userID, info.Email, p.id)

	if flow.LoginID != "" {
		if postLoginRedirect != "" {
			// Web BFF flow — redirect browser back to the app with the token and LLM key.
			dest := postLoginRedirect + "?token=" + rawToken
			if llmKey != "" {
				dest += "&llm_key=" + llmKey
			}
			http.Redirect(w, r, dest, http.StatusFound)
			return
		}
		// CLI polling flow — render a friendly browser page; token goes to the poller.
		s.renderCallbackSuccess(w, info.Email)
	} else {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{"session_token": rawToken}
		if llmKey != "" {
			resp["llm_key"] = llmKey
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// renderCallbackSuccess renders a success page for a CLI-initiated browser login.
func (s *Server) renderCallbackSuccess(w http.ResponseWriter, email string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, callbackSuccessHTML, email)
}

// renderCallbackError renders an error page for a failed browser login.
func (s *Server) renderCallbackError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, callbackErrorHTML, msg)
}

// renderZulipLinkedPage shows a success page after a Zulip account link.
func (s *Server) renderZulipLinkedPage(w http.ResponseWriter, zulipEmail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, zulipLinkedHTML, zulipEmail)
}

const zulipLinkedHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Zulip account linked</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d27;border:1px solid #2d3148;border-radius:12px;padding:2.5rem;text-align:center;max-width:380px}
h1{font-size:1.2rem;margin-bottom:.75rem}p{color:#94a3b8;font-size:.9rem;line-height:1.5}
.email{color:#a5b4fc;font-family:monospace}</style></head>
<body><div class="card"><h1>&#10003; Zulip account linked</h1>
<p>Your Zulip account <span class="email">%s</span> has been linked.<br>
You may close this tab and return to Zulip.</p></div></body></html>`

const callbackSuccessHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Signed in</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d27;border:1px solid #2d3148;border-radius:12px;padding:2.5rem;text-align:center;max-width:360px}
h1{font-size:1.2rem;margin-bottom:.75rem}p{color:#94a3b8;font-size:.9rem}</style></head>
<body><div class="card"><h1>&#10003; Signed in</h1><p>Authenticated as <strong>%s</strong>.</p><p style="margin-top:.75rem">You may close this tab and return to the terminal.</p></div></body></html>`

const callbackErrorHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Authentication failed</title>
<style>body{font-family:system-ui,sans-serif;background:#0f1117;color:#e2e8f0;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d27;border:1px solid #7f1d1d;border-radius:12px;padding:2.5rem;text-align:center;max-width:360px}
h1{font-size:1.2rem;margin-bottom:.75rem;color:#fca5a5}p{color:#94a3b8;font-size:.9rem}</style></head>
<body><div class="card"><h1>&#10007; Authentication failed</h1><p>%s</p></div></body></html>`

// ── POST /token/verify ────────────────────────────────────────────────────────
//
// Validates a session token issued by authd. Used by agentd and bridges to
// authenticate inbound requests without calling upstream providers.
//
// Request:  Authorization: Bearer <session_token>
// Response: 200 { "user_id": "...", "email": "...", "expires_at": "..." }
//           401 { "error": "..." }
func (s *Server) handleTokenVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}

	// Rate-limit by remote IP to slow token brute-forcing.
	ip := remoteIP(r)
	if ok, reason := s.verifyLimiter.Allow(ip); !ok {
		logging.Warnf("authd: verify: rate limit exceeded for %s", ip)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded: "+reason, http.StatusTooManyRequests)
		return
	}

	raw := bearerToken(r)
	if raw == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing Bearer token"})
		return
	}

	sess, err := s.db.ValidateSession(r.Context(), raw)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		if errors.Is(err, authdb.ErrExpired) {
			logging.Warnf("authd: verify: expired token from %s", r.RemoteAddr)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
		} else {
			logging.Warnf("authd: verify: invalid token from %s", r.RemoteAddr)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
		}
		return
	}

	user, err := s.db.GetUser(r.Context(), sess.UserID)
	if err != nil {
		logging.Warnf("authd: verify: get user %s: %v", sess.UserID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":    user.ID,
		"email":      user.Email,
		"name":       user.DisplayName,
		"expires_at": sess.ExpiresAt.Format(time.RFC3339),
		"is_service": user.ProviderID == "service",
	})
}

// ── GET /account/me ───────────────────────────────────────────────────────────
//
// Returns the authenticated user's profile and linked bridge services.
// Requires a valid Bearer session token.
//
// Response: { "user_id", "email", "name", "created_at", "links": { "zulip": ["email@..."] } }
func (s *Server) handleAccountMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}

	userID, ok := s.authedUserID(w, r)
	if !ok {
		return
	}

	user, err := s.db.GetUser(r.Context(), userID)
	if err != nil {
		logging.Warnf("authd: account/me: get user %s: %v", userID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	zulipEmails, err := s.db.ListZulipLinksByUserID(r.Context(), userID)
	if err != nil {
		logging.Warnf("authd: account/me: list zulip links: %v", err)
		zulipEmails = nil
	}
	if zulipEmails == nil {
		zulipEmails = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":    user.ID,
		"email":      user.Email,
		"name":       user.DisplayName,
		"created_at": user.CreatedAt.Format(time.RFC3339),
		"links": map[string]interface{}{
			"zulip": zulipEmails,
		},
	})
}

// ── POST /token/service ───────────────────────────────────────────────────────
//
// Issues a long-lived service account token for headless clients (bridges, etc.).
// Requires the admin token in the Authorization header.
//
// Request body: { "service_id": "zulip-bridge", "ttl_hours": 8760 }
// Response:     { "session_token": "..." }
func (s *Server) handleTokenService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}
	if s.adminToken == "" {
		http.Error(w, "admin token not configured (set AUTHD_ADMIN_TOKEN)", http.StatusServiceUnavailable)
		return
	}
	if bearerToken(r) != s.adminToken {
		logging.Warnf("authd: service token: unauthorized attempt from %s", r.RemoteAddr)
		w.Header().Set("WWW-Authenticate", `Bearer realm="authd"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ServiceID string `json:"service_id"`
		TTLHours  int    `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ServiceID == "" {
		http.Error(w, "service_id is required", http.StatusBadRequest)
		return
	}

	ttl := s.serviceTokenTTL()
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
	}

	// Upsert a synthetic user record for the service account.
	userID, err := s.db.UpsertUser(r.Context(), "service", req.ServiceID, req.ServiceID+"@service", req.ServiceID)
	if err != nil {
		logging.Warnf("authd: service token: upsert service user: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rawToken, _, err := s.db.CreateSession(r.Context(), userID, ttl)
	if err != nil {
		logging.Warnf("authd: service token: create session: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	logging.Infof("authd: issued service token for %q (TTL %s) requested from %s", req.ServiceID, ttl, r.RemoteAddr)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"session_token": rawToken})
}

// ── GET /.well-known/jwks.json ────────────────────────────────────────────────
//
// Placeholder JWKS endpoint. Currently authd uses opaque tokens validated via
// /token/verify. This endpoint is reserved for future JWT migration.
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"keys":[]}`))
}

// isAllowedRedirect reports whether redirectURL's origin is in the allowlist.
func (s *Server) isAllowedRedirect(redirectURL string) bool {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	origin := u.Scheme + "://" + u.Host
	for _, allowed := range s.allowedRedirectOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

// parseAllowedOrigins splits a comma-separated list of origins.
// e.g. "http://localhost:3000,https://app.example.com"
func parseAllowedOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// bearerToken extracts the Bearer token from the Authorization header.
func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// verifyBearerToken returns nil if the token is a valid authd session.
func (s *Server) verifyBearerToken(ctx context.Context, raw string) error {
	_, err := s.db.ValidateSession(ctx, raw)
	return err
}

// remoteIP returns the best-effort client IP for rate limiting.
// Prefers X-Forwarded-For (set by reverse proxies) over RemoteAddr.
func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may be "client, proxy1, proxy2"; take the first.
		if idx := strings.Index(xff, ","); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// Strip the port from RemoteAddr ("1.2.3.4:5678" → "1.2.3.4").
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx >= 0 {
		return r.RemoteAddr[:idx]
	}
	return r.RemoteAddr
}
