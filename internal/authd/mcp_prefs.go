package authd

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/logging"
)

// ── GET /account/mcp-prefs ────────────────────────────────────────────────────
//
// Returns the authenticated user's MCP server and tool preferences.
// Response: { "servers": { "filesystem": false }, "tools": { "mcp_filesystem_write_file": false } }
func (s *Server) handleGetMCPPrefs(w http.ResponseWriter, r *http.Request) {
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

	prefs, err := s.db.GetMCPPrefs(r.Context(), userID)
	if err != nil {
		logging.Warnf("authd: get mcp prefs for %s: %v", userID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prefs)
}

// ── PUT /account/mcp-prefs/servers/:name ─────────────────────────────────────
//
// Sets the enabled state for an MCP server.
// Body: { "enabled": false }
func (s *Server) handleSetMCPServerPref(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}

	serverName := strings.TrimPrefix(r.URL.Path, "/account/mcp-prefs/servers/")
	if serverName == "" {
		http.Error(w, "missing server name", http.StatusBadRequest)
		return
	}

	userID, ok := s.authedUserID(w, r)
	if !ok {
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if err := s.db.SetMCPServerPref(r.Context(), userID, serverName, body.Enabled); err != nil {
		logging.Warnf("authd: set mcp server pref %s for %s: %v", serverName, userID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── PUT /account/mcp-prefs/tools/:name ───────────────────────────────────────
//
// Sets the enabled state for an individual MCP tool.
// Body: { "enabled": false }
func (s *Server) handleSetMCPToolPref(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}

	toolName := strings.TrimPrefix(r.URL.Path, "/account/mcp-prefs/tools/")
	if toolName == "" {
		http.Error(w, "missing tool name", http.StatusBadRequest)
		return
	}

	userID, ok := s.authedUserID(w, r)
	if !ok {
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if err := s.db.SetMCPToolPref(r.Context(), userID, toolName, body.Enabled); err != nil {
		logging.Warnf("authd: set mcp tool pref %s for %s: %v", toolName, userID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// authedUserID validates the Bearer token and returns the user ID.
// Writes an error response and returns false if authentication fails.
func (s *Server) authedUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := bearerToken(r)
	if raw == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	sess, err := s.db.ValidateSession(r.Context(), raw)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return sess.UserID, true
}
