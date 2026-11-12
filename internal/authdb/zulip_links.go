package authdb

import (
	"context"
	"errors"
	"fmt"
)

// ErrZulipAlreadyLinked is returned by CreateZulipLink when the Zulip address
// is already linked to a different user.
var ErrZulipAlreadyLinked = errors.New("authdb: zulip address is already linked to another account")

// CreateZulipLink stores the mapping from a Zulip sender email to an authd
// user_id. It never re-points an existing link at a different user: that
// returns ErrZulipAlreadyLinked, and the old link must be removed first
// (!unlink in Zulip, or auth-admin). Re-linking to the same user refreshes
// linked_at.
func (db *DB) CreateZulipLink(ctx context.Context, zulipEmail, userID string) error {
	const q = `
		INSERT INTO zulip_links (zulip_email, user_id)
		VALUES ($1, $2)
		ON CONFLICT (zulip_email) DO UPDATE
		  SET linked_at=now()
		  WHERE zulip_links.user_id = EXCLUDED.user_id`
	tag, err := db.pool.Exec(ctx, q, zulipEmail, userID)
	if err != nil {
		return fmt.Errorf("authdb: create zulip link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrZulipAlreadyLinked
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
