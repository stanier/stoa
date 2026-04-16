package apikeyd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "__session"

type ctxKey int

const ctxUserID ctxKey = iota

// UserIDFromContext returns the authenticated user ID attached by requireAuth.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// requireAuth is middleware that validates the __session cookie against authd.
// On success it attaches the user ID to the request context.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r)
		if token == "" {
			log.Printf("apikeyd: requireAuth: no token on %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		userID, err := s.verifySessionToken(r.Context(), token)
		if err != nil || userID == "" {
			log.Printf("apikeyd: requireAuth: verify failed on %s %s: err=%v userID=%q token_len=%d", r.Method, r.URL.Path, err, userID, len(token))
			http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tokenFromRequest extracts the raw session token from either the __session
// cookie (browser) or the Authorization: Bearer header (webd proxy).
func tokenFromRequest(r *http.Request) string {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	return tokenFromCookie(r)
}

// tokenFromCookie extracts the raw session token from the __session cookie.
func tokenFromCookie(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// verifySessionToken calls authd's /token/verify endpoint and returns the user ID.
func (s *Server) verifySessionToken(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.AuthdURL+"/token/verify", http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("authd verify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authd verify: status %d", resp.StatusCode)
	}

	var info struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("authd verify: decode: %w", err)
	}
	return info.UserID, nil
}

// ─── Login / callback / logout — mirrors webd exactly ─────────────────────

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
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "authd unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.URL == "" {
		http.Error(w, "bad authd response", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, result.URL, http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	secure := len(s.cfg.ExternalURL) >= 8 && s.cfg.ExternalURL[:8] == "https://"
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}
