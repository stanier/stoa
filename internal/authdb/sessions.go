package authdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Session represents a row in the sessions table.
type Session struct {
	TokenHash  string
	UserID     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt *time.Time
}

// CreateSession generates a new opaque session token for userID and persists it.
// ttl is how long the token is valid. Returns the raw token (send to client)
// and the session record.
func (db *DB) CreateSession(ctx context.Context, userID string, ttl time.Duration) (rawToken string, sess *Session, err error) {
	raw, err := generateToken()
	if err != nil {
		return "", nil, fmt.Errorf("authdb: generate token: %w", err)
	}
	hash := hashToken(raw)
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	const q = `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $3)`

	if _, err := db.pool.Exec(ctx, q, hash, userID, now, expiresAt); err != nil {
		return "", nil, fmt.Errorf("authdb: create session: %w", err)
	}

	sess = &Session{
		TokenHash: hash,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	return raw, sess, nil
}

// ValidateSession looks up the session for rawToken, updates last_seen_at,
// and returns the session if valid and unexpired.
// Returns ErrNotFound if the token doesn't exist, ErrExpired if it has expired.
func (db *DB) ValidateSession(ctx context.Context, rawToken string) (*Session, error) {
	hash := hashToken(rawToken)
	const q = `
		UPDATE sessions
		SET last_seen_at = now()
		WHERE token_hash = $1 AND expires_at > now()
		RETURNING token_hash, user_id, created_at, expires_at, last_seen_at`

	s := &Session{}
	err := db.pool.QueryRow(ctx, q, hash).Scan(
		&s.TokenHash, &s.UserID, &s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt,
	)
	if err == pgx.ErrNoRows {
		// Distinguish expired from never-existed by checking without the expiry filter.
		var exists bool
		_ = db.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash = $1)`, hash,
		).Scan(&exists)
		if exists {
			return nil, ErrExpired
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: validate session: %w", err)
	}
	return s, nil
}

// RevokeSession deletes the session for rawToken. Idempotent.
func (db *DB) RevokeSession(ctx context.Context, rawToken string) error {
	hash := hashToken(rawToken)
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	if err != nil {
		return fmt.Errorf("authdb: revoke session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes all sessions whose expires_at is in the past.
// Safe to call periodically as a maintenance task.
func (db *DB) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("authdb: delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// HasActiveSession reports whether the user with the given UUID has at least
// one session that has not yet expired.
func (db *DB) HasActiveSession(ctx context.Context, userID string) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sessions WHERE user_id=$1 AND expires_at > now())`,
		userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("authdb: has active session: %w", err)
	}
	return exists, nil
}

// ErrExpired is returned when a token exists but has passed its expiry time.
var ErrExpired = fmt.Errorf("authdb: session expired")

// generateToken returns a cryptographically random 32-byte base64url-encoded token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken returns the hex-encoded SHA-256 of raw. This is what gets stored in Postgres.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
