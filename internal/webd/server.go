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
)

//go:embed all:dist
var staticFiles embed.FS

// Server is the webd HTTP server.
type Server struct {
	cfg        *Config
	mux        *http.ServeMux
	httpClient *http.Client
}

// NewServer constructs a fully-wired Server.
func NewServer(cfg *Config) (*Server, error) {
	s := &Server{
		cfg:        cfg,
		mux:        http.NewServeMux(),
		httpClient: &http.Client{},
	}
	s.registerRoutes()
	return s, nil
}

// Router returns the HTTP handler.
func (s *Server) Router() http.Handler { return s.mux }

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
