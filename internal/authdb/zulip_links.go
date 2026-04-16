package authdb

import (
	"context"
	"fmt"
)

// CreateZulipLink stores or replaces the mapping from a Zulip sender email
// to an authd user_id. Re-linking overwrites the previous entry.
func (db *DB) CreateZulipLink(ctx context.Context, zulipEmail, userID string) error {
	const q = `
		INSERT INTO zulip_links (zulip_email, user_id)
		VALUES ($1, $2)
		ON CONFLICT (zulip_email) DO UPDATE
		  SET user_id=$2, linked_at=now()`
	if _, err := db.pool.Exec(ctx, q, zulipEmail, userID); err != nil {
		return fmt.Errorf("authdb: create zulip link: %w", err)
	}
	return nil
}

// ResolveZulipLink returns the authd user_id for the given Zulip email,
// or ErrNotFound if no link exists.
func (db *DB) ResolveZulipLink(ctx context.Context, zulipEmail string) (string, error) {
	var userID string
	err := db.pool.QueryRow(ctx,
		`SELECT user_id FROM zulip_links WHERE zulip_email=$1`, zulipEmail,
	).Scan(&userID)
	if err != nil {
		return "", ErrNotFound
	}
	return userID, nil
}

// ListZulipLinksByUserID returns all Zulip emails linked to the given authd user_id.
func (db *DB) ListZulipLinksByUserID(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT zulip_email FROM zulip_links WHERE user_id=$1 ORDER BY linked_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("authdb: list zulip links: %w", err)
	}
	defer rows.Close()
	var emails []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, fmt.Errorf("authdb: scan zulip link: %w", err)
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}

// DeleteZulipLink removes the link for a Zulip email. Idempotent.
func (db *DB) DeleteZulipLink(ctx context.Context, zulipEmail string) error {
	_, err := db.pool.Exec(ctx,
		`DELETE FROM zulip_links WHERE zulip_email=$1`, zulipEmail)
	return err
}
