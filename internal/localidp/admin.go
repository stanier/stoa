package localidp

import (
	"context"
	"fmt"
)

// AdminListUsers opens a DB connection and returns all local users.
// Intended for use by auth-admin; does not require a running server.
func AdminListUsers(ctx context.Context, dsn string) ([]LocalUser, error) {
	d, err := openDB(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer d.close()
	return d.ListUsers(ctx)
}

// AdminCreateUser opens a DB connection and creates a new local user account.
// Returns the new user's ID.
func AdminCreateUser(ctx context.Context, dsn, username, email, displayName, password string) (string, error) {
	d, err := openDB(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer d.close()
	id, err := d.CreateUser(ctx, username, email, displayName, password)
	if err != nil {
		return "", fmt.Errorf("localidp: create user: %w", err)
	}
	return id, nil
}

// AdminResetPassword opens a DB connection and resets a user's password.
func AdminResetPassword(ctx context.Context, dsn, username, newPassword string) error {
	d, err := openDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer d.close()
	return d.ResetPassword(ctx, username, newPassword)
}

// AdminUnlockUser opens a DB connection and clears the account lockout.
func AdminUnlockUser(ctx context.Context, dsn, username string) error {
	d, err := openDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer d.close()
	return d.UnlockAccount(ctx, username)
}

// AdminDeleteUser opens a DB connection and deletes the user account.
func AdminDeleteUser(ctx context.Context, dsn, username string) error {
	d, err := openDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer d.close()
	return d.DeleteUser(ctx, username)
}
