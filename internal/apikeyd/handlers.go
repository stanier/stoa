package apikeyd

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"
)

// ─── Key management handlers ───────────────────────────────────────────────

type keyResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

func recordToResponse(k KeyRecord) keyResponse {
	return keyResponse{
		ID:         k.ID,
		Name:       k.Name,
		Prefix:     k.Prefix,
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		ExpiresAt:  k.ExpiresAt,
		RevokedAt:  k.RevokedAt,
	}
}

// GET /api/keys — list all keys for the authenticated user.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())

	keys, err := s.db.ListKeys(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list keys")
		return
	}

	resp := make([]keyResponse, 0, len(keys))
	for _, k := range keys {
		resp = append(resp, recordToResponse(k))
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/keys — create a new API key.
//
// Request body:
//
//	{ "name": "my app", "expires_in_days": 90 }  // expires_in_days is optional
//
// Response includes the raw key exactly once:
//
//	{ "key": "stoa-infgw_...", "id": "...", "name": "...", "prefix": "...", "created_at": "..." }
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	userID := UserIDFromContext(r.Context())

	var body struct {
		Name          string `json:"name"`
		ExpiresInDays *int   `json:"expires_in_days,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	var expiresAt *time.Time
	if body.ExpiresInDays != nil && *body.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, *body.ExpiresInDays)
		expiresAt = &t
	}

	rawKey, rec, err := s.db.CreateKey(r.Context(), userID, body.Name, expiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"key":        rawKey, // shown exactly once
		"id":         rec.ID,
		"name":       rec.Name,
		"prefix":     rec.Prefix,
		"created_at": rec.CreatedAt,
		"expires_at": rec.ExpiresAt,
	})
}

// DELETE /api/keys/{id} — revoke a key.
func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	userID := UserIDFromContext(r.Context())
	keyID := strings.TrimPrefix(r.URL.Path, "/api/keys/")
	if keyID == "" {
		writeError(w, http.StatusBadRequest, "key ID required")
		return
	}

	if err := s.db.RevokeKey(r.Context(), keyID, userID); err != nil {
		if err == ErrNotFound {
			writeError(w, http.StatusNotFound, "key not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to revoke key")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ─── Admin endpoint (called by internal services with APIKEYD_ADMIN_TOKEN) ──

// POST /admin/users/{user_id}/key
//
// Atomically revokes any existing active service key for the user and issues a
// fresh one. The raw key is returned exactly once — the caller must store it.
// Subsequent calls for the same user rotate the key.
func (s *Server) handleAdminProvisionKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract user_id from /admin/users/{user_id}/key
	userID := path.Base(path.Dir(r.URL.Path))
	if userID == "" || userID == "." || userID == "users" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	const serviceName = "agentd-service"
	rawKey, rec, err := s.db.ProvisionServiceKey(r.Context(), userID, serviceName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to provision key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"key":        rawKey,
		"id":         rec.ID,
		"user_id":    rec.UserID,
		"prefix":     rec.Prefix,
		"created_at": rec.CreatedAt,
	})
}

// ─── Verification endpoint (called by inference-gateway, not browsers) ─────────────

// POST /keys/verify — validate an API key and return key metadata.
// The raw key is passed as Authorization: Bearer <key> or x-api-key: <key>.
// No session cookie required — the key IS the credential.
func (s *Server) handleVerifyKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rawKey := extractBearerKey(r)
	if rawKey == "" {
		writeError(w, http.StatusUnauthorized, "missing API key")
		return
	}

	rec, err := s.db.VerifyKey(r.Context(), rawKey)
	if err != nil {
		switch err {
		case ErrNotFound:
			writeError(w, http.StatusUnauthorized, "invalid API key")
		case ErrRevoked:
			writeError(w, http.StatusUnauthorized, "API key has been revoked")
		case ErrExpired:
			writeError(w, http.StatusUnauthorized, "API key has expired")
		default:
			writeError(w, http.StatusInternalServerError, "verification failed")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key_id":     rec.ID,
		"user_id":    rec.UserID,
		"name":       rec.Name,
		"expires_at": rec.ExpiresAt,
	})
}

// GET /api/me — return the current user's identity (proxied from authd).
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	token := tokenFromCookie(r)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		s.cfg.AuthdURL+"/token/verify", http.NoBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "authd unavailable")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewDecoder(resp.Body).Decode(new(interface{})) // consume
}

// GET /healthz
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── helpers ───────────────────────────────────────────────────────────────

// extractBearerKey returns the raw API key from Authorization: Bearer or x-api-key headers.
func extractBearerKey(r *http.Request) string {
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
