// Package apikeyd is the data access layer for the API key management service.
package apikeyd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	dbutil "github.com/stanier/stoa/internal/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const RequiredSchemaVersion = 1

const keyPrefix = "stoa-infgw_"

var (
	ErrNotFound    = errors.New("apikeyd: key not found")
	ErrRevoked     = errors.New("apikeyd: key has been revoked")
	ErrExpired     = errors.New("apikeyd: key has expired")
	ErrUnauthorized = errors.New("apikeyd: unauthorized")
)

// DB wraps a pgxpool connection pool with API key operations.
type DB struct {
	pool *pgxpool.Pool
}

// KeyRecord is a row from the api_keys table.
type KeyRecord struct {
	ID          string
	UserID      string
	Name        string
	Prefix      string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

// IsActive returns true when the key has not been revoked and has not expired.
func (k *KeyRecord) IsActive() bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return false
	}
	return true
}

// Open connects to Postgres, runs any pending goose migrations, and returns a DB.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := dbutil.OpenPool(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("apikeyd: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	if err := runMigrations(sqlDB); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apikeyd: migrate: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close releases the connection pool.
func (d *DB) Close() { d.pool.Close() }

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(db, "migrations")
}

// CreateKey generates a new API key for the given user, stores its hash, and
// returns the raw key (shown once) plus the display record.
// expiresAt may be nil for a non-expiring key.
func (d *DB) CreateKey(ctx context.Context, userID, name string, expiresAt *time.Time) (rawKey string, rec *KeyRecord, err error) {
	raw, err := generateRawKey()
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: generate key: %w", err)
	}

	hash := hashKey(raw)
	prefix := displayPrefix(raw)

	var id string
	var createdAt time.Time

	err = d.pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, key_hash, prefix, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, userID, name, hash, prefix, expiresAt).Scan(&id, &createdAt)
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: insert key: %w", err)
	}

	return raw, &KeyRecord{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}

// ProvisionServiceKey is an admin-only operation that atomically revokes any
// existing active key for (userID, name) and creates a fresh one, returning
// the raw key value. It is idempotent in the sense that a second call
// for the same user simply rotates the key. The raw key is returned exactly
// once — callers must persist it themselves.
func (d *DB) ProvisionServiceKey(ctx context.Context, userID, name string) (rawKey string, rec *KeyRecord, err error) {
	raw, err := generateRawKey()
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: generate key: %w", err)
	}
	hash := hashKey(raw)
	prefix := displayPrefix(raw)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	// Revoke existing active key for this (user_id, name), if any.
	_, err = tx.Exec(ctx, `
		UPDATE api_keys SET revoked_at = now()
		WHERE user_id = $1 AND name = $2 AND revoked_at IS NULL
	`, userID, name)
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: revoke existing service key: %w", err)
	}

	var id string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, key_hash, prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`, userID, name, hash, prefix).Scan(&id, &createdAt)
	if err != nil {
		return "", nil, fmt.Errorf("apikeyd: insert service key: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("apikeyd: commit: %w", err)
	}

	return raw, &KeyRecord{
		ID:        id,
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: createdAt,
	}, nil
}

// ListKeys returns all non-revoked keys for a user, newest first.
func (d *DB) ListKeys(ctx context.Context, userID string) ([]KeyRecord, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT id, user_id, name, prefix, created_at, last_used_at, expires_at, revoked_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("apikeyd: list keys: %w", err)
	}
	defer rows.Close()

	var keys []KeyRecord
	for rows.Next() {
		var k KeyRecord
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix,
			&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, fmt.Errorf("apikeyd: scan key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeKey marks a key as revoked. userID is checked so users can only revoke their own keys.
func (d *DB) RevokeKey(ctx context.Context, keyID, userID string) error {
	tag, err := d.pool.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, keyID, userID)
	if err != nil {
		return fmt.Errorf("apikeyd: revoke key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// VerifyKey looks up a raw key, validates it, updates last_used_at, and returns the record.
func (d *DB) VerifyKey(ctx context.Context, rawKey string) (*KeyRecord, error) {
	hash := hashKey(rawKey)

	var k KeyRecord
	err := d.pool.QueryRow(ctx, `
		SELECT id, user_id, name, prefix, created_at, last_used_at, expires_at, revoked_at
		FROM api_keys
		WHERE key_hash = $1
	`, hash).Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("apikeyd: verify key: %w", err)
	}

	if k.RevokedAt != nil {
		return nil, ErrRevoked
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return nil, ErrExpired
	}

	// Update last_used_at asynchronously — don't fail the request if this errors.
	go func() {
		_, _ = d.pool.Exec(context.Background(), `
			UPDATE api_keys SET last_used_at = now() WHERE id = $1
		`, k.ID)
	}()

	return &k, nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func generateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func hashKey(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(h[:])
}

// displayPrefix returns the first 8 characters after the "stoa-infgw_" prefix,
// giving users enough to identify which key a request used.
func displayPrefix(rawKey string) string {
	body := rawKey
	if len(rawKey) > len(keyPrefix) {
		body = rawKey[len(keyPrefix):]
	}
	if len(body) > 8 {
		return keyPrefix + body[:8] + "..."
	}
	return rawKey
}
