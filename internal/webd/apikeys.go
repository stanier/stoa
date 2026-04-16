package webd

import (
	"io"
	"net/http"
	"strings"
)

// handleAPIKeysProxy proxies /api/apikeys[/*] to apikeyd's /api/keys[/*],
// injecting the session Bearer token so apikeyd can verify the caller via authd.
func (s *Server) handleAPIKeysProxy(w http.ResponseWriter, r *http.Request) {
	if s.cfg.APIKeydURL == "" {
		http.Error(w, `{"error":"apikeyd not configured"}`, http.StatusServiceUnavailable)
		return
	}

	// Map /api/apikeys → /api/keys and /api/apikeys/{id} → /api/keys/{id}
	tail := strings.TrimPrefix(r.URL.Path, "/api/apikeys")
	target := s.cfg.APIKeydURL + "/api/keys" + tail
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Forward content-type for POST requests.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	// Inject session token as Bearer so apikeyd can validate via authd.
	token := s.tokenFromCookie(r)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"apikeyd unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
