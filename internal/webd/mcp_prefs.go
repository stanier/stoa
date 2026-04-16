package webd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/logging"
)

// handleGetMCPPrefs proxies GET /api/mcp-prefs → authd /account/mcp-prefs.
func (s *Server) handleGetMCPPrefs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	s.proxyToAuthd(w, r, s.cfg.AuthdURL+"/account/mcp-prefs", token)
}

// handleMCPPrefsProxy proxies PUT /api/mcp-prefs/servers/:name and
// PUT /api/mcp-prefs/tools/:name to the equivalent authd paths.
func (s *Server) handleMCPPrefsProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	// Strip /api prefix: /api/mcp-prefs/servers/foo → /account/mcp-prefs/servers/foo
	authdPath := "/account" + strings.TrimPrefix(r.URL.Path, "/api")
	s.proxyToAuthd(w, r, s.cfg.AuthdURL+authdPath, token)
}

// proxyToAuthd forwards an HTTP request to authd, injecting the Bearer token.
func (s *Server) proxyToAuthd(w http.ResponseWriter, r *http.Request, targetURL, token string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		logging.Errorf("webd: proxy to authd %s: %v", targetURL, err)
		http.Error(w, "auth service unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// mcpPrefs holds the parsed user MCP preferences from authd.
type mcpPrefs struct {
	Servers map[string]bool `json:"servers"`
	Tools   map[string]bool `json:"tools"`
}

// toolInfo is a single entry from agentd GET /tools.
type toolInfo struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// computeAllowedTools fetches the user's MCP prefs and the full tool catalogue
// from agentd, then returns the list of tool names the user has enabled.
// Returns nil (meaning "all tools") if prefs are unavailable or all tools are enabled.
func (s *Server) computeAllowedTools(ctx context.Context, token string) []string {
	// Fetch prefs from authd.
	prefsReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.cfg.AuthdURL+"/account/mcp-prefs", nil)
	if err != nil {
		return nil
	}
	prefsReq.Header.Set("Authorization", "Bearer "+token)
	prefsResp, err := s.httpClient.Do(prefsReq)
	if err != nil || prefsResp.StatusCode != http.StatusOK {
		return nil // fail open
	}
	defer prefsResp.Body.Close()

	var prefs mcpPrefs
	if err := json.NewDecoder(prefsResp.Body).Decode(&prefs); err != nil {
		return nil
	}
	if len(prefs.Servers) == 0 && len(prefs.Tools) == 0 {
		return nil // no restrictions
	}

	// Fetch tool catalogue from agentd.
	toolsReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.cfg.AgentdURL+"/tools", nil)
	if err != nil {
		return nil
	}
	toolsReq.Header.Set("Authorization", "Bearer "+token)
	toolsResp, err := s.httpClient.Do(toolsReq)
	if err != nil || toolsResp.StatusCode != http.StatusOK {
		return nil // fail open
	}
	defer toolsResp.Body.Close()

	var tools []toolInfo
	if err := json.NewDecoder(toolsResp.Body).Decode(&tools); err != nil {
		return nil
	}

	// Compute which tools to allow.
	// Built-in tools are always included. MCP tools are included if:
	//   - their server is not explicitly disabled, AND
	//   - the tool itself is not explicitly disabled
	// An individual tool pref overrides the server pref.
	allowed := make([]string, 0, len(tools))
	anyRestricted := false

	for _, t := range tools {
		if t.Source == "builtin" {
			allowed = append(allowed, t.Name)
			continue
		}
		// Source is "mcp_<servername>" — extract the server name.
		serverName := strings.TrimPrefix(t.Source, "mcp_")

		// Determine enabled state: tool pref > server pref > default (true).
		enabled := true
		if serverEnabled, hasServerPref := prefs.Servers[serverName]; hasServerPref {
			enabled = serverEnabled
		}
		if toolEnabled, hasToolPref := prefs.Tools[t.Name]; hasToolPref {
			enabled = toolEnabled // individual tool overrides server
		}

		if enabled {
			allowed = append(allowed, t.Name)
		} else {
			anyRestricted = true
		}
	}

	if !anyRestricted {
		return nil // all enabled, no filter needed
	}

	return allowed
}
