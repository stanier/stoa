package agentd

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/stanier/stoa/internal/apikeydclient"
	"github.com/stanier/stoa/internal/authdb"
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

// AuthMiddleware returns HTTP middleware that enforces authentication.
//
// Priority order:
//  1. authdClient non-nil (preferred) — validates Bearer tokens via authd's
//     /token/verify endpoint. User ID is attached to request context.
//  2. db non-nil (legacy) — validates Bearer session tokens directly against
//     Postgres. Kept for backwards compatibility during the authd migration.
//  3. apiKey non-empty — shared API key check (local/dev mode).
//  4. All nil/empty — open access; a startup warning is logged.
//
// Auth endpoints (/auth/*) bypass all checks so unauthenticated clients can
// initiate flows. The /healthz endpoint is always open.
//
// In all modes, tokens with the "llmp_" prefix are additionally validated
// against apikeyd when an apikeydClient is provided. This allows external
// ACP callers to authenticate with per-caller API keys.
func AuthMiddleware(apiKey string, db *authdb.DB, next http.Handler) http.Handler {
	return AuthMiddlewareWithAuthd(apiKey, db, nil, next)
}

// AuthMiddlewareWithAuthd is the full constructor used when authd is configured.
func AuthMiddlewareWithAuthd(apiKey string, db *authdb.DB, ac *authdclient.Client, next http.Handler) http.Handler {
	return AuthMiddlewareWithAPIKeyd(apiKey, db, ac, nil, next)
}

// AuthMiddlewareWithAPIKeyd is the full constructor that also supports apikeyd
// for validating external ACP caller API keys (llmp_ prefix).
func AuthMiddlewareWithAPIKeyd(apiKey string, db *authdb.DB, ac *authdclient.Client, akc *apikeydclient.Client, next http.Handler) http.Handler {
	if ac != nil {
		return authdMiddlewareWithAPIKeyd(ac, akc, next)
	}
	if db != nil {
		return oauth2Middleware(db, next)
	}
	return apiKeyMiddleware(apiKey, next)
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
// /healthz, /acp/ping, and /metrics are always open.
func isExempt(path string) bool {
	return strings.HasPrefix(path, "/auth/") || path == "/healthz" || path == "/acp/ping" || path == "/metrics"
}

// oauth2Middleware validates the Bearer session token against Postgres.
func oauth2Middleware(db *authdb.DB, next http.Handler) http.Handler {
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

		sess, err := db.ValidateSession(r.Context(), raw)
		if err != nil {
			if err == authdb.ErrExpired {
				http.Error(w, "session expired", http.StatusUnauthorized)
			} else {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserID, sess.UserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// apiKeyMiddleware validates a shared API key — local/dev fallback.
func apiKeyMiddleware(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	bearer := "Bearer " + apiKey
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(auth), []byte(bearer)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// WarnIfNoAuth logs a startup warning when agentd is running without any authentication.
func WarnIfNoAuth(apiKey string, oauthEnabled bool, authdURL string) {
	if authdURL != "" || oauthEnabled || apiKey != "" {
		return
	}
	logging.Warnf("agentd: no authentication configured — all endpoints are publicly accessible. " +
		"Set AGENTD_AUTHD_URL, --api-key / AGENTD_API_KEY, or enable auth.enabled in config.")
}

// APIKeyFromEnv reads AGENTD_API_KEY from the environment.
func APIKeyFromEnv() string {
	return strings.TrimSpace(envOr("AGENTD_API_KEY", ""))
}

// RequireAPIKey is kept for backwards compatibility with cmd/agentd/main.go.
// New code should use AuthMiddleware.
func RequireAPIKey(apiKey string, next http.Handler) http.Handler {
	return AuthMiddleware(apiKey, nil, next)
}
