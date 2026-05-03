package apikeyd

import (
	"fmt"
	"net/http"
)

// Server is the apikeyd HTTP server.
type Server struct {
	cfg        *Config
	db         *DB
	mux        *http.ServeMux
	httpClient *http.Client
}

// NewServer constructs a fully-wired Server.
func NewServer(cfg *Config, db *DB) *Server {
	s := &Server{
		cfg:        cfg,
		db:         db,
		mux:        http.NewServeMux(),
		httpClient: &http.Client{},
	}
	s.registerRoutes()
	return s
}

// ListenAndServe starts the HTTP server on the configured port.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	return http.ListenAndServe(addr, s.mux)
}

func (s *Server) registerRoutes() {
	// ── Auth flow (no session required) ──────────────────────────────────
	s.mux.HandleFunc("/auth/login", s.handleLogin)
	s.mux.HandleFunc("/auth/callback", s.handleCallback)
	s.mux.HandleFunc("/auth/logout", s.handleLogout)

	// ── Key verification (called by inference-gateway, no session cookie) ────────
	s.mux.HandleFunc("/keys/verify", s.handleVerifyKey)

	// ── Admin endpoints (APIKEYD_ADMIN_TOKEN required) ────────────────────
	s.mux.Handle("/admin/users/", s.requireAdminToken(http.HandlerFunc(s.handleAdminProvisionKey)))

	// ── Health check ──────────────────────────────────────────────────────
	s.mux.HandleFunc("/healthz", s.handleHealthz)

	// ── Authenticated API ─────────────────────────────────────────────────
	s.mux.Handle("/api/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	s.mux.Handle("/api/keys", s.requireAuth(http.HandlerFunc(s.handleKeys)))
	s.mux.Handle("/api/keys/", s.requireAuth(http.HandlerFunc(s.handleKeys)))
}

// handleKeys dispatches GET / POST / DELETE on /api/keys[/{id}].
func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListKeys(w, r)
	case http.MethodPost:
		s.handleCreateKey(w, r)
	case http.MethodDelete:
		s.handleRevokeKey(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
