// Package webd is the web UI backend-for-frontend (BFF).
//
// It serves the Svelte SPA, handles the OIDC browser login flow via authd,
// manages HttpOnly session cookies, and proxies WebSocket + REST calls to
// agentd — injecting the Bearer token so the browser never sees it directly.
package webd

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

//go:embed all:dist
var staticFiles embed.FS

// Server is the webd HTTP server.
type Server struct {
	cfg        *Config
	mux        *http.ServeMux
	httpClient *http.Client
	wsUpgrader websocket.Upgrader
	s3         *s3Clients // nil when S3 is not configured (local-dev fallback)
}

// NewServer constructs a fully-wired Server.
func NewServer(cfg *Config) (*Server, error) {
	allowedOrigin := strings.TrimRight(cfg.ExternalURL, "/")
	s := &Server{
		cfg:        cfg,
		mux:        http.NewServeMux(),
		httpClient: &http.Client{},
		wsUpgrader: websocket.Upgrader{
			// Only allow WebSocket upgrades from the configured external origin.
			// Non-browser clients (e.g. TUI) send no Origin header and are allowed through.
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimRight(r.Header.Get("Origin"), "/")
				return origin == "" || origin == allowedOrigin
			},
		},
	}

	if cfg.S3.Endpoint != "" {
		sc, err := newS3Clients(cfg.S3)
		if err != nil {
			return nil, err
		}
		s.s3 = sc
	}

	s.registerRoutes()
	return s, nil
}

// Router returns the HTTP handler wrapped with security headers and CSRF protection.
func (s *Server) Router() http.Handler { return securityHeaders(csrfMiddleware(s.mux)) }

// securityHeaders adds defensive HTTP headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Svelte injects inline styles; everything else is same-origin only.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; "+
				"connect-src 'self' ws: wss:; "+
				"font-src 'self'; "+
				"object-src 'none'; "+
				"frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerRoutes() {
	// Auth endpoints — no session required.
	s.mux.HandleFunc("/auth/login", s.handleLogin)
	s.mux.HandleFunc("/auth/callback", s.handleCallback)
	s.mux.HandleFunc("/auth/logout", s.handleLogout)

	// API — session required.
	s.mux.Handle("/api/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	s.mux.Handle("/api/account", s.requireAuth(http.HandlerFunc(s.handleAccount)))
	s.mux.Handle("/api/mcp-prefs", s.requireAuth(http.HandlerFunc(s.handleGetMCPPrefs)))
	s.mux.Handle("/api/mcp-prefs/", s.requireAuth(http.HandlerFunc(s.handleMCPPrefsProxy)))
	s.mux.Handle("/api/tools", s.requireAuth(http.HandlerFunc(s.handleToolsProxy)))
	s.mux.Handle("/api/upload", s.requireAuth(http.HandlerFunc(s.handleUpload)))
	s.mux.Handle("/api/sessions", s.requireAuth(http.HandlerFunc(s.handleSessionsProxy)))
	s.mux.Handle("/api/sessions/", s.requireAuth(http.HandlerFunc(s.handleSessionsProxy)))
	s.mux.Handle("/api/models", s.requireAuth(http.HandlerFunc(s.handleModelsProxy)))
	s.mux.Handle("/api/apikeys", s.requireAuth(http.HandlerFunc(s.handleAPIKeysProxy)))
	s.mux.Handle("/api/apikeys/", s.requireAuth(http.HandlerFunc(s.handleAPIKeysProxy)))
	s.mux.Handle("/api/agents", s.requireAuth(http.HandlerFunc(s.handleAgentsProxy)))
	s.mux.Handle("/api/agents/", s.requireAuth(http.HandlerFunc(s.handleAgentsProxy)))
	s.mux.Handle("/api/projects", s.requireAuth(http.HandlerFunc(s.handleProjectsProxy)))
	s.mux.Handle("/api/projects/", s.requireAuth(http.HandlerFunc(s.handleProjectsProxy)))

	// WebSocket proxy — session validated inside handler.
	s.mux.HandleFunc("/ws/", s.handleWSProxy)

	// SPA static files — serve index.html for all unmatched paths (client-side routing).
	distFS, _ := fs.Sub(staticFiles, "dist")
	fileServer := http.FileServer(http.FS(distFS))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve known static assets directly; fall back to index.html for all
		// other paths so the Svelte router handles client-side navigation.
		path := r.URL.Path
		if path != "/" {
			_, err := fs.Stat(distFS, path[1:])
			if err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback — serve index.html.
		r2 := *r
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, &r2)
	})
}
