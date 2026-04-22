package authdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Role represents a row in the roles table.
type Role struct {
	ID           string
	Name         string
	Persona      string
	AllowedTools []string
	CreatedAt    time.Time
}

// UserRole represents a user's role assignment.
type UserRole struct {
	UserID     string
	RoleID     string
	RoleName   string
	Persona    string
	AllowedTools []string
	AssignedAt time.Time
}

// CreateRole inserts a new role. Returns the new role's UUID.
func (db *DB) CreateRole(ctx context.Context, name, persona string, allowedTools []string) (string, error) {
	if allowedTools == nil {
		allowedTools = []string{}
	}
	toolsJSON, err := json.Marshal(allowedTools)
	if err != nil {
		return "", fmt.Errorf("authdb: marshal allowed_tools: %w", err)
	}
	var id string
	err = db.pool.QueryRow(ctx,
		`INSERT INTO roles (name, persona, allowed_tools) VALUES ($1, $2, $3) RETURNING id`,
		name, persona, toolsJSON,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("authdb: create role: %w", err)
	}
	return id, nil
}

// DeleteRole removes a role by name. Returns ErrNotFound if it doesn't exist.
// Fails if any users are currently assigned to this role (FK RESTRICT).
func (db *DB) DeleteRole(ctx context.Context, name string) error {
	tag, err := db.pool.Exec(ctx, `DELETE FROM roles WHERE name=$1`, name)
	if err != nil {
		return fmt.Errorf("authdb: delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRoles returns all roles ordered by name.
func (db *DB) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, name, persona, allowed_tools, created_at FROM roles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("authdb: list roles: %w", err)
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		var toolsJSON []byte
		if err := rows.Scan(&r.ID, &r.Name, &r.Persona, &toolsJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("authdb: scan role: %w", err)
		}
		_ = json.Unmarshal(toolsJSON, &r.AllowedTools)
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// AssignRole assigns roleName to userID, replacing any existing assignment.
func (db *DB) AssignRole(ctx context.Context, userID, roleName string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, id FROM roles WHERE name=$2
		ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id, assigned_at = now()`,
		userID, roleName,
	)
	if err != nil {
		return fmt.Errorf("authdb: assign role: %w", err)
	}
	return nil
}

// RemoveRole removes the role assignment for userID. Idempotent.
func (db *DB) RemoveRole(ctx context.Context, userID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1`, userID)
	if err != nil {
		return fmt.Errorf("authdb: remove role: %w", err)
	}
	return nil
}

// GetUserRole returns the role assigned to userID, or ErrNotFound if none.
func (db *DB) GetUserRole(ctx context.Context, userID string) (*UserRole, error) {
	ur := &UserRole{UserID: userID}
	var toolsJSON []byte
	err := db.pool.QueryRow(ctx, `
		SELECT ur.role_id, r.name, r.persona, r.allowed_tools, ur.assigned_at
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1`,
		userID,
	).Scan(&ur.RoleID, &ur.RoleName, &ur.Persona, &toolsJSON, &ur.AssignedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authdb: get user role: %w", err)
	}
	_ = json.Unmarshal(toolsJSON, &ur.AllowedTools)
	return ur, nil
}
