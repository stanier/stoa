// Package localidp implements a minimal OIDC identity provider backed by
// local username/password accounts.
//
// It exposes the standard OIDC discovery + token endpoints so that authd
// treats it identically to any upstream provider (Google, GitLab, etc.).
//
// WARNING: local-idp is intended for development and self-hosted single-tenant
// deployments only. It has not been audited for multi-tenant or public-facing
// production use. For production, prefer a dedicated identity provider
// (Keycloak, Authentik, Okta, Google, etc.) configured as an authd upstream.
package localidp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/logging"
)

// Server is the local-idp HTTP server.
type Server struct {
	issuer      string // internal base URL — used for iss claim, token/userinfo endpoints
	externalURL string // browser-reachable base URL — used only for authorization_endpoint
	clientID    string
	allowedRedirectURIs []string
	db          *db
	codes       *codeStore
	signingKey  *signingKey
	accessTokens sync.Map
	loginTmpl   *template.Template
	mux         *http.ServeMux
}

// NewServer constructs and wires up a Server.
//   - issuer:      internal base URL authd uses for OIDC discovery and token/userinfo calls
//                  (e.g. "http://local-idp:9999")
//   - externalURL: browser-reachable base URL for the authorization_endpoint only
//                  (e.g. "http://localhost:9999"). May equal issuer in single-host deployments.
//   - allowedRedirectURIs: registered redirect URIs enforced on every authorize request.
func NewServer(ctx context.Context, issuer, externalURL, clientID, dsn string, allowedRedirectURIs []string) (*Server, error) {
	if externalURL == "" {
		externalURL = issuer
	}
	if dsn == "" {
		return nil, fmt.Errorf("localidp: LOCAL_IDP_DATABASE_URL is required")
	}

	database, err := openDB(ctx, dsn)
	if err != nil {
		return nil, err
	}

	key, err := database.loadOrCreateSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	logging.Infof("local-idp: signing key loaded (kid=%s)", key.kid)

	s := &Server{
		issuer:              strings.TrimRight(issuer, "/"),
		externalURL:         strings.TrimRight(externalURL, "/"),
		clientID:            clientID,
		allowedRedirectURIs: allowedRedirectURIs,
		db:                  database,
		codes:               newCodeStore(),
		signingKey:          key,
		loginTmpl:           template.Must(template.New("login").Parse(loginHTML)),
		mux:                 http.NewServeMux(),
	}

	go s.reapAccessTokens()
	s.registerRoutes()
	return s, nil
}

func (s *Server) Router() http.Handler { return s.mux }

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/.well-known/openid-configuration", s.handleDiscovery)
	s.mux.HandleFunc("/oauth/jwks", s.handleJWKS)
	s.mux.HandleFunc("/oauth/authorize", s.handleAuthorize)
	s.mux.HandleFunc("/oauth/token", s.handleToken)
	s.mux.HandleFunc("/oauth/userinfo", s.handleUserinfo)
	s.mux.HandleFunc("/healthz", s.handleHealthz)
}

// ── GET /.well-known/openid-configuration ─────────────────────────────────────

func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := map[string]interface{}{
		// issuer, token/userinfo/jwks endpoints use the internal URL so authd
		// can reach them from inside the container network.
		"issuer":           s.issuer,
		"token_endpoint":   s.issuer + "/oauth/token",
		"userinfo_endpoint": s.issuer + "/oauth/userinfo",
		"jwks_uri":         s.issuer + "/oauth/jwks",
		// authorization_endpoint uses the external URL so the browser can reach
		// the login form. In single-host deployments these are the same.
		"authorization_endpoint":                s.externalURL + "/oauth/authorize",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "profile"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"S256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// ── GET /oauth/jwks ────────────────────────────────────────────────────────────

func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	data, err := jwksJSON(s.signingKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// ── GET /oauth/authorize ──────────────────────────────────────────────────────
//
// Validates the request parameters, then either renders the login form (GET)
// or processes credentials (POST via form submission).

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	// Parse query params (same for GET and POST since the form POSTs back here).
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	challengeMethod := q.Get("code_challenge_method")

	if clientID != s.clientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if redirectURI == "" {
		http.Error(w, "redirect_uri required", http.StatusBadRequest)
		return
	}
	if !s.isAllowedRedirectURI(redirectURI) {
		http.Error(w, "redirect_uri not registered", http.StatusBadRequest)
		return
	}
	if codeChallenge == "" || challengeMethod != "S256" {
		http.Error(w, "PKCE S256 is required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderLoginForm(w, r, redirectURI, state, codeChallenge, "")
	case http.MethodPost:
		s.processLogin(w, r, redirectURI, state, codeChallenge)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) renderLoginForm(w http.ResponseWriter, r *http.Request, redirectURI, state, codeChallenge, errMsg string) {
	// Build the form action URL server-side so all query parameters are
	// properly URL-encoded and the template never handles raw user input
	// in an attribute position.
	params := url.Values{}
	params.Set("client_id", s.clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("response_type", "code")
	formAction := "/oauth/authorize?" + params.Encode()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_ = s.loginTmpl.Execute(w, loginData{
		FormAction: formAction,
		Error:      errMsg,
	})
}

// isAllowedRedirectURI reports whether uri is in the registered allowlist.
func (s *Server) isAllowedRedirectURI(uri string) bool {
	for _, allowed := range s.allowedRedirectURIs {
		if allowed == uri {
			return true
		}
	}
	return false
}

func (s *Server) processLogin(w http.ResponseWriter, r *http.Request, redirectURI, state, codeChallenge string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	user, err := s.db.Authenticate(r.Context(), username, password)
	if err != nil {
		var msg string
		switch {
		case errors.Is(err, ErrAccountLocked):
			msg = "Account is locked. Contact an administrator."
		case errors.Is(err, ErrUserNotFound), errors.Is(err, ErrInvalidPassword):
			msg = "Invalid username or password."
		default:
			logging.Warnf("local-idp: authenticate error: %v", err)
			msg = "An error occurred. Please try again."
		}
		s.renderLoginForm(w, r, redirectURI, state, codeChallenge, msg)
		return
	}

	code, err := s.codes.issue(&authCode{
		userID:        user.ID,
		sub:           user.ID,
		email:         user.Email,
		name:          user.DisplayName,
		clientID:      s.clientID,
		redirectURI:   redirectURI,
		codeChallenge: codeChallenge,
	})
	if err != nil {
		logging.Errorf("local-idp: issue auth code: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	dest, _ := url.Parse(redirectURI)
	q := dest.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	dest.RawQuery = q.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

// ── POST /oauth/token ─────────────────────────────────────────────────────────

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request", "could not parse form")
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType != "authorization_code" {
		tokenError(w, "unsupported_grant_type", grantType)
		return
	}

	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")
	redirectURI := r.FormValue("redirect_uri")

	entry, err := s.codes.consume(code, codeVerifier)
	if err != nil {
		tokenError(w, "invalid_grant", err.Error())
		return
	}
	if entry.redirectURI != redirectURI {
		tokenError(w, "invalid_grant", "redirect_uri mismatch")
		return
	}

	const idTokenTTL = 1 * time.Hour
	idToken, err := issueIDToken(s.signingKey, s.issuer, s.clientID,
		entry.sub, entry.email, entry.name, idTokenTTL)
	if err != nil {
		logging.Errorf("local-idp: issue id_token: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	accessToken, err := issueOpaqueToken()
	if err != nil {
		logging.Errorf("local-idp: issue access token: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.accessTokens.Store(accessToken, accessTokenEntry{
		sub:       entry.sub,
		email:     entry.email,
		name:      entry.name,
		expiresAt: time.Now().Add(idTokenTTL),
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   int(idTokenTTL.Seconds()),
		"id_token":     idToken,
	})
}

// ── GET /oauth/userinfo ───────────────────────────────────────────────────────

func (s *Server) handleUserinfo(w http.ResponseWriter, r *http.Request) {
	raw := bearerToken(r)
	if raw == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="local-idp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	val, ok := s.accessTokens.Load(raw)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	entry := val.(accessTokenEntry)
	if time.Now().After(entry.expiresAt) {
		s.accessTokens.Delete(raw)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sub":                entry.sub,
		"email":              entry.email,
		"name":               entry.name,
		"preferred_username": entry.name,
	})
}

// ── GET /healthz ───────────────────────────────────────────────────────────────

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(v, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func tokenError(w http.ResponseWriter, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": desc,
	})
}

func (s *Server) reapAccessTokens() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.accessTokens.Range(func(k, v interface{}) bool {
			if now.After(v.(accessTokenEntry).expiresAt) {
				s.accessTokens.Delete(k)
			}
			return true
		})
	}
}

// ── Login form ─────────────────────────────────────────────────────────────────

type loginData struct {
	FormAction string // pre-built, URL-encoded form action; never assembled in template
	Error      string
}

// envOr returns the env var value or the fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: system-ui, -apple-system, sans-serif;
    background: #0f1117;
    color: #e2e8f0;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
  }
  .card {
    background: #1a1d27;
    border: 1px solid #2d3148;
    border-radius: 12px;
    padding: 2.5rem;
    width: 100%;
    max-width: 380px;
  }
  h1 { font-size: 1.25rem; font-weight: 600; margin-bottom: 1.75rem; text-align: center; }
  label { display: block; font-size: 0.8rem; color: #94a3b8; margin-bottom: 0.35rem; }
  input[type=text], input[type=password] {
    width: 100%;
    padding: 0.6rem 0.75rem;
    background: #0f1117;
    border: 1px solid #2d3148;
    border-radius: 6px;
    color: #e2e8f0;
    font-size: 0.95rem;
    outline: none;
    margin-bottom: 1.1rem;
    transition: border-color 0.15s;
  }
  input:focus { border-color: #6366f1; }
  button {
    width: 100%;
    padding: 0.65rem;
    background: #6366f1;
    color: #fff;
    border: none;
    border-radius: 6px;
    font-size: 0.95rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s;
  }
  button:hover { background: #4f46e5; }
  .error {
    background: #3b1515;
    border: 1px solid #7f1d1d;
    border-radius: 6px;
    color: #fca5a5;
    font-size: 0.85rem;
    padding: 0.6rem 0.75rem;
    margin-bottom: 1.1rem;
  }
</style>
</head>
<body>
<div class="card">
  <h1>Sign in</h1>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <form method="POST" action="{{.FormAction}}">
    <label for="username">Username</label>
    <input id="username" name="username" type="text" autocomplete="username" autofocus required>
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Sign in</button>
  </form>
</div>
</body>
</html>`
