package agentd

import (
	"context"
	"errors"
	"fmt"
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
// authd's /token/verify endpoint. Tokens with the "stoa-infgw_" prefix are additionally
// checked against apikeyd when an apikeydClient is provided.
//
// When authdClient is nil the handler is served without authentication. This only
// happens when the caller has already validated the config via CheckAuthConfig and
// the operator explicitly set AGENTD_NO_AUTH=true.
//
// Auth endpoints (/auth/*) and /healthz bypass all checks so unauthenticated
// clients can reach them.
func AuthMiddlewareWithAPIKeyd(ac *authdclient.Client, akc *apikeydclient.Client, next http.Handler) http.Handler {
	if ac != nil {
		return authdMiddlewareWithAPIKeyd(ac, akc, next)
	}
	return next // open access — CheckAuthConfig already enforced AGENTD_NO_AUTH=true at startup
}

// authdMiddlewareWithAPIKeyd validates Bearer tokens by calling authd's /token/verify.
// When akc is non-nil, tokens with the "stoa-infgw_" prefix are also tried against apikeyd,
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

		// External ACP caller API keys (stoa-infgw_ prefix) are validated via apikeyd.
		if akc != nil && strings.HasPrefix(raw, "stoa-infgw_") {
			keyInfo, err := akc.VerifyKey(r.Context(), raw)
			if err == nil {
				ctx := context.WithValue(r.Context(), ctxUserID, keyInfo.UserID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// Fall through to authd — the token may have been issued by authd
			// and happen to start with stoa-infgw_ (unlikely but handled gracefully).
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
	return strings.HasPrefix(path, "/auth/") || path == "/healthz" || path == "/acp/ping" || path == "/metrics"
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// CheckAuthConfig validates the authentication configuration at startup.
//
// Returns nil when either:
//   - authdURL is set (production auth via authd), or
//   - noAuth is "true" (explicit opt-out for local dev — logs a prominent warning).
//
// Returns an error in all other cases so the caller can refuse to start rather than
// silently serving all endpoints unauthenticated.
func CheckAuthConfig(authdURL, noAuth string) error {
	if authdURL != "" {
		return nil
	}
	if noAuth == "true" {
		logging.Warnf("agentd: AGENTD_NO_AUTH=true — authentication is disabled, all endpoints are publicly accessible. Do not use in production.")
		return nil
	}
	return fmt.Errorf("no authentication configured: set AGENTD_AUTHD_URL to enable auth, or set AGENTD_NO_AUTH=true to explicitly run without auth (local dev only)")
}
