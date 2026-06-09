package webd

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

const csrfCookie = "__csrf"

// newCSRFToken generates a 32-byte random hex CSRF token.
func newCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// setCSRFCookie writes the __csrf double-submit cookie.
// The cookie is intentionally non-HttpOnly so the SPA can read and echo it
// back as X-CSRF-Token on mutating requests. The Same-Origin Policy prevents
// cross-origin scripts from reading it. SameSite=Strict prevents it being
// sent on cross-origin navigations.
func setCSRFCookie(w http.ResponseWriter, secure bool) (string, error) {
	token, err := newCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
	return token, nil
}

// clearCSRFCookie expires the __csrf cookie on logout.
func clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   csrfCookie,
		Path:   "/",
		MaxAge: -1,
	})
}

// csrfMiddleware implements the double-submit cookie pattern.
//
// GET, HEAD, and OPTIONS are exempt (no side effects).
// /auth/login and /auth/callback are exempt — they run before the CSRF cookie
// exists and are protected by the OAuth2 state parameter instead.
// All other methods must supply an X-CSRF-Token header matching the __csrf cookie.
func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/auth/login" || r.URL.Path == "/auth/callback" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(csrfCookie)
		if err != nil || cookie.Value == "" {
			http.Error(w, "csrf validation failed", http.StatusForbidden)
			return
		}
		if r.Header.Get("X-CSRF-Token") != cookie.Value {
			http.Error(w, "csrf validation failed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
