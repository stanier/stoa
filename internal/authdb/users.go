package authdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// User represents a row in the users table.
type User struct {
	ID          string
	ProviderID  string
	Sub         string
	Email       string
	DisplayName string
	CreatedAt   time.Time
	LastSeenAt  *time.Time
}

// UpsertUser inserts a new user or updates email/display_name and last_seen_at
// for an existing (provider_id, sub) pair. Returns the user's UUID.
func (db *DB) UpsertUser(ctx context.Context, providerID, sub, email, displayName string) (string, error) {
	const q = `
		INSERT INTO users (provider_id, sub, email, display_name, last_seen_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (provider_id, sub) DO UPDATE
			SET email        = EXCLUDED.email,
			    display_name = EXCLUDED.display_name,
			    last_seen_at = now()
		RETURNING id`

	var id string
	if err := db.pool.QueryRow(ctx, q, providerID, sub, email, displayName).Scan(&id); err != nil {
		return "", fmt.Errorf("authdb: upsert user: %w", err)
	}
	return id, nil
}

// GetUser returns the user with the given UUID, or ErrNotFound if absent.
func (db *DB) GetUser(ctx context.Context, userID string) (*User, error) {
	const q = `
		SELECT id, provider_id, sub, email, display_name, created_at, last_seen_at
		FROM users WHERE id = $1`

	u := &User{}
	err := db.pool.QueryRow(ctx, q, userID).Scan(
		&u.ID, &u.ProviderID, &u.Sub, &u.Email, &u.DisplayName, &u.CreatedAt, &u.LastSeenAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: get user: %w", err)
	}
	return u, nil
}

// ListUsers returns all users ordered by email. Used by auth-admin.
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	const q = `SELECT id, provider_id, sub, email, display_name, created_at, last_seen_at
	            FROM users ORDER BY email`
	rows, err := db.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("authdb: list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.ProviderID, &u.Sub, &u.Email, &u.DisplayName, &u.CreatedAt, &u.LastSeenAt); err != nil {
			return nil, fmt.Errorf("authdb: scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// RevokeAllSessionsForEmail deletes all sessions for the user with the given email.
// Returns the number of sessions revoked.
func (db *DB) RevokeAllSessionsForEmail(ctx context.Context, email string) (int64, error) {
	const q = `DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE email = $1)`
	tag, err := db.pool.Exec(ctx, q, email)
	if err != nil {
		return 0, fmt.Errorf("authdb: revoke sessions for %s: %w", email, err)
	}
	return tag.RowsAffected(), nil
}

// UserWithRole extends User with an optional role name.
type UserWithRole struct {
	User
	RoleName string // empty if no role assigned
}

// ListUsersWithRoles returns all users with their assigned role name (if any), ordered by email.
func (db *DB) ListUsersWithRoles(ctx context.Context) ([]UserWithRole, error) {
	const q = `
		SELECT u.id, u.provider_id, u.sub, u.email, u.display_name, u.created_at, u.last_seen_at,
		       coalesce(r.name, '') AS role_name
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles r ON r.id = ur.role_id
		ORDER BY u.email`
	rows, err := db.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("authdb: list users with roles: %w", err)
	}
	defer rows.Close()

	var users []UserWithRole
	for rows.Next() {
		var u UserWithRole
		if err := rows.Scan(
			&u.ID, &u.ProviderID, &u.Sub, &u.Email, &u.DisplayName, &u.CreatedAt, &u.LastSeenAt,
			&u.RoleName,
		); err != nil {
			return nil, fmt.Errorf("authdb: scan user with role: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ErrNotFound is returned when a queried row does not exist.
var ErrNotFound = fmt.Errorf("authdb: not found")
