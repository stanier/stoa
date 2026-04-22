package localidp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

const (
	// argon2id parameters — OWASP recommended minimums.
	argonTime    = 2
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	maxFailedAttempts = 10
)

// LocalUser is a user record from the local_users table.
type LocalUser struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
	CreatedAt   time.Time
}

var (
	ErrUserNotFound    = errors.New("localidp: user not found")
	ErrInvalidPassword = errors.New("localidp: invalid password")
	ErrAccountLocked   = errors.New("localidp: account locked")
)

// CreateUser inserts a new user and hashed password. Returns the new user's ID.
func (d *db) CreateUser(ctx context.Context, username, email, displayName, password string) (string, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return "", fmt.Errorf("localidp: hash password: %w", err)
	}

	id := uuid.New().String()
	if displayName == "" {
		displayName = username
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("localidp: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const insUser = `INSERT INTO local_users (id, username, email, display_name) VALUES ($1, $2, $3, $4)`
	if _, err := tx.Exec(ctx, insUser, id, username, email, displayName); err != nil {
		return "", fmt.Errorf("localidp: insert user: %w", err)
	}

	const insCred = `INSERT INTO local_credentials (user_id, password_hash) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, insCred, id, hash); err != nil {
		return "", fmt.Errorf("localidp: insert credentials: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("localidp: commit: %w", err)
	}
	return id, nil
}

// Authenticate verifies username + password. On success returns the user.
// Increments failed_attempts on wrong password; locks after maxFailedAttempts.
func (d *db) Authenticate(ctx context.Context, username, password string) (*LocalUser, error) {
	const q = `
		SELECT u.id, u.username, u.email, u.display_name, u.created_at,
		       c.password_hash, c.failed_attempts, c.locked_at
		FROM local_users u
		JOIN local_credentials c ON c.user_id = u.id
		WHERE u.username = $1`

	var (
		user          LocalUser
		passwordHash  string
		failedAttempts int
		lockedAt      *time.Time
	)
	err := d.pool.QueryRow(ctx, q, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.DisplayName, &user.CreatedAt,
		&passwordHash, &failedAttempts, &lockedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("localidp: authenticate query: %w", err)
	}

	if lockedAt != nil {
		return nil, ErrAccountLocked
	}

	if !verifyPassword(password, passwordHash) {
		// Increment failed attempts; lock if threshold reached.
		newFailed := failedAttempts + 1
		if newFailed >= maxFailedAttempts {
			_, _ = d.pool.Exec(ctx,
				`UPDATE local_credentials SET failed_attempts=$1, locked_at=now(), updated_at=now() WHERE user_id=$2`,
				newFailed, user.ID)
		} else {
			_, _ = d.pool.Exec(ctx,
				`UPDATE local_credentials SET failed_attempts=$1, updated_at=now() WHERE user_id=$2`,
				newFailed, user.ID)
		}
		return nil, ErrInvalidPassword
	}

	// Successful auth — reset failed attempts.
	_, _ = d.pool.Exec(ctx,
		`UPDATE local_credentials SET failed_attempts=0, updated_at=now() WHERE user_id=$1`,
		user.ID)

	return &user, nil
}

// ResetPassword replaces the password hash for a user. Admin operation.
func (d *db) ResetPassword(ctx context.Context, username, newPassword string) error {
	hash, err := hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("localidp: hash password: %w", err)
	}
	tag, err := d.pool.Exec(ctx,
		`UPDATE local_credentials c SET password_hash=$1, failed_attempts=0, locked_at=NULL, updated_at=now()
		 FROM local_users u WHERE c.user_id=u.id AND u.username=$2`,
		hash, username)
	if err != nil {
		return fmt.Errorf("localidp: reset password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UnlockAccount clears the locked_at flag and resets failed_attempts. Admin operation.
func (d *db) UnlockAccount(ctx context.Context, username string) error {
	tag, err := d.pool.Exec(ctx,
		`UPDATE local_credentials c SET failed_attempts=0, locked_at=NULL, updated_at=now()
		 FROM local_users u WHERE c.user_id=u.id AND u.username=$1`,
		username)
	if err != nil {
		return fmt.Errorf("localidp: unlock account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ListUsers returns all local users ordered by username.
func (d *db) ListUsers(ctx context.Context) ([]LocalUser, error) {
	const q = `SELECT id, username, email, display_name, created_at FROM local_users ORDER BY username`
	rows, err := d.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("localidp: list users: %w", err)
	}
	defer rows.Close()

	var users []LocalUser
	for rows.Next() {
		var u LocalUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("localidp: scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// DeleteUser removes a user and their credentials. Admin operation.
func (d *db) DeleteUser(ctx context.Context, username string) error {
	tag, err := d.pool.Exec(ctx, `DELETE FROM local_users WHERE username=$1`, username)
	if err != nil {
		return fmt.Errorf("localidp: delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ── Password hashing (argon2id) ───────────────────────────────────────────────

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 {
		return false
	}

	// Parse the stored parameters so verification works even if the constants
	// are bumped in a future release.
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	actual := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
