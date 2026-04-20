package agentd

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/apikeydclient"
	"github.com/stanier/stoa/internal/authdclient"
	"github.com/stanier/stoa/internal/logging"
)

// contextKey is the unexported type used for request context values.
type contextKey int

const (
	// ctxUserID is the request context key for the authenticated user's UUID.
	ctxUserID contextKey = iota
	// ctxIsService indicates the token belongs to a service account.
	ctxIsService
)

// UserIDFromContext returns the authenticated user ID attached by AuthMiddleware.
// Returns ("", false) when the request is not authenticated via OAuth.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxUserID).(string)
	return v, ok && v != ""
}

// IsServiceFromContext reports whether the authenticated caller is a service account.
// Service accounts bypass session ownership checks.
func IsServiceFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(ctxIsService).(bool)
	return v
}

// AuthMiddlewareWithAPIKeyd wraps the handler with authentication enforcement.
//
// When authdClient is non-nil (production), all requests are validated against
// authd's /token/verify endpoint. Tokens with the "llmp_" prefix are additionally
// checked against apikeyd when an apikeydClient is provided.
//
// When authdClient is nil (local dev, no AGENTD_AUTHD_URL set), the handler is
// served without authentication — a warning is logged at startup via WarnIfNoAuth.
//
// Auth endpoints (/auth/*) and /healthz bypass all checks so unauthenticated
// clients can reach them.
func AuthMiddlewareWithAPIKeyd(ac *authdclient.Client, akc *apikeydclient.Client, next http.Handler) http.Handler {
	if ac != nil {
		return authdMiddlewareWithAPIKeyd(ac, akc, next)
	}
	return next // open access — startup warning already emitted by WarnIfNoAuth
}

// authdMiddlewareWithAPIKeyd validates Bearer tokens by calling authd's /token/verify.
// When akc is non-nil, tokens with the "llmp_" prefix are also tried against apikeyd,
// allowing external ACP callers to authenticate with per-caller API keys.
func authdMiddlewareWithAPIKeyd(ac *authdclient.Client, akc *apikeydclient.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		raw := bearerToken(r)
		if raw == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// External ACP caller API keys (llmp_ prefix) are validated via apikeyd.
		if akc != nil && strings.HasPrefix(raw, "llmp_") {
			keyInfo, err := akc.VerifyKey(r.Context(), raw)
			if err == nil {
				ctx := context.WithValue(r.Context(), ctxUserID, keyInfo.UserID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// Fall through to authd — the token may have been issued by authd
			// and happen to start with llmp_ (unlikely but handled gracefully).
		}

		info, err := ac.Verify(r.Context(), raw)
		if err != nil {
			if errors.Is(err, authdclient.ErrExpired) {
				http.Error(w, "session expired", http.StatusUnauthorized)
			} else {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserID, info.UserID)
		ctx = context.WithValue(ctx, ctxIsService, info.IsService)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isExempt reports whether the request path bypasses authentication.
// /auth/* endpoints are exempt so clients can initiate flows.
// /healthz and /acp/ping are always open.
func isExempt(path string) bool {
	return strings.HasPrefix(path, "/auth/") || path == "/healthz" || path == "/acp/ping"
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// WarnIfNoAuth logs a startup warning when agentd is running without authentication.
func WarnIfNoAuth(authdURL string) {
	if authdURL != "" {
		return
	}
	logging.Warnf("agentd: no authentication configured — all endpoints are publicly accessible. " +
		"Set AGENTD_AUTHD_URL to enable authentication.")
}
