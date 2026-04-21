package webd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// UserIDFromContext returns the authenticated user ID from the request context.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

const (
	sessionCookie = "__session"
	llmKeyCookie  = "__llm_key"
)

// startLogin initiates the browser auth flow by calling authd's /oauth/login
// with a post_login_redirect pointing back to webd's /auth/callback.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")

	body, _ := json.Marshal(map[string]string{
		"provider_id":         providerID,
		"post_login_redirect": s.cfg.ExternalURL + "/auth/callback",
	})

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		s.cfg.AuthdURL+"/oauth/login", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		http.Error(w, "auth service unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		http.Error(w, "auth error: "+string(data), http.StatusBadGateway)
		return
	}

	var loginResp struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil || loginResp.URL == "" {
		http.Error(w, "invalid auth response", http.StatusBadGateway)
		return
	}

	http.Redirect(w, r, loginResp.URL, http.StatusFound)
}

// handleCallback receives the browser redirect from authd after a successful
// login. authd appends ?token=<session_token> to the post_login_redirect URL.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	// Verify the token with authd before trusting it.
	if err := s.verifyToken(r.Context(), token); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	secure := strings.HasPrefix(s.cfg.ExternalURL, "https")
	maxAge := int((24 * time.Hour).Seconds())

	// Store session token as an HttpOnly, SameSite=Lax cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   maxAge,
	})

	// Store the per-user LLM key (provisioned by authd at login) in its own
	// HttpOnly cookie. Injected into agentd SessionConfig at session creation.
	if llmKey := r.URL.Query().Get("llm_key"); llmKey != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     llmKeyCookie,
			Value:    llmKey,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
			MaxAge:   maxAge,
		})
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLogout clears the session cookie and redirects to the home page.
// Requires POST to prevent logout CSRF via navigations (<a>, <img>, etc.).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     llmKeyCookie,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleMe validates the session cookie and returns the user info from authd.
// Used by the SPA to check auth state on load.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		s.cfg.AuthdURL+"/token/verify", nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, resp.Body)
}

type contextKey int

const ctxUserID contextKey = iota

// requireAuth is middleware that rejects requests without a valid session cookie.
// On success it attaches the authd user_id to the request context.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.tokenFromCookie(r)
		if token == "" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		// Attach user_id to context for downstream handlers.
		if userID := s.userIDFromToken(r.Context(), token); userID != "" {
			r = r.WithContext(context.WithValue(r.Context(), ctxUserID, userID))
		}
		next.ServeHTTP(w, r)
	})
}

// userIDFromToken calls authd to get the user_id for a session token.
func (s *Server) userIDFromToken(ctx context.Context, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.AuthdURL+"/token/verify", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	defer resp.Body.Close()
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Printf("webd: userIDFromToken: decode response: %v", err)
	}
	return body.UserID
}

// tokenFromCookie returns the authd session token from the session cookie, or "".
func (s *Server) tokenFromCookie(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// llmKeyFromCookie returns the per-user llmp_* key from the LLM key cookie, or "".
func (s *Server) llmKeyFromCookie(r *http.Request) string {
	c, err := r.Cookie(llmKeyCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// handleAccount proxies to authd /account/me, which returns the user's profile
// and linked bridge services (e.g., Zulip emails).
func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		s.cfg.AuthdURL+"/account/me", nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, resp.Body)
}

// verifyToken calls authd's /token/verify and returns nil on success.
func (s *Server) verifyToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.AuthdURL+"/token/verify", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webd: token verify returned %d", resp.StatusCode)
	}
	return nil
}
