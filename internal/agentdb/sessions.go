package agentdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Session is a row from agentd_sessions.
type Session struct {
	ID                string
	UserID            string // empty when anonymous/service
	Persona           string
	Source            string
	Title             string
	CreatedAt         time.Time
	LastActiveAt      time.Time
	LastInputTokens   int
	TotalOutputTokens int
}

// CreateSession inserts a new session record.
func (d *DB) CreateSession(ctx context.Context, id, userID, persona, source string) error {
	const q = `
		INSERT INTO agentd_sessions (id, user_id, persona, source)
		VALUES ($1, NULLIF($2,''), $3, $4)
		ON CONFLICT (id) DO NOTHING`
	if _, err := d.pool.Exec(ctx, q, id, userID, persona, source); err != nil {
		return fmt.Errorf("agentdb: create session: %w", err)
	}
	return nil
}

// ListSessions returns sessions ordered by last_active_at descending.
//
// When userID is non-empty, returns sessions owned by that user (user_id=$1)
// plus sessions with no owner (user_id IS NULL). Sessions owned by a different
// user are never returned. Pass userID="" to return all sessions (service accounts).
func (d *DB) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	const qAll = `
		SELECT id, COALESCE(user_id,''), persona, source, title, created_at, last_active_at,
		       last_input_tokens, total_output_tokens
		FROM agentd_sessions ORDER BY last_active_at DESC`
	const qUser = `
		SELECT id, COALESCE(user_id,''), persona, source, title, created_at, last_active_at,
		       last_input_tokens, total_output_tokens
		FROM agentd_sessions
		WHERE user_id=$1 OR user_id IS NULL
		ORDER BY last_active_at DESC`

	if userID == "" {
		r, err := d.pool.Query(ctx, qAll)
		if err != nil {
			return nil, fmt.Errorf("agentdb: list sessions: %w", err)
		}
		defer r.Close()
		return scanSessions(r)
	}
	r, err := d.pool.Query(ctx, qUser, userID)
	if err != nil {
		return nil, fmt.Errorf("agentdb: list sessions: %w", err)
	}
	defer r.Close()
	return scanSessions(r)
}

// UpdateStats updates token counts and last_active_at after each turn.
func (d *DB) UpdateStats(ctx context.Context, id string, lastInputTokens, totalOutputTokens int) error {
	const q = `
		UPDATE agentd_sessions
		SET last_active_at=now(), last_input_tokens=$2, total_output_tokens=$3
		WHERE id=$1`
	if _, err := d.pool.Exec(ctx, q, id, lastInputTokens, totalOutputTokens); err != nil {
		return fmt.Errorf("agentdb: update stats: %w", err)
	}
	return nil
}

// TouchLastActive updates last_active_at without changing other fields.
func (d *DB) TouchLastActive(ctx context.Context, id string) error {
	_, err := d.pool.Exec(ctx, `UPDATE agentd_sessions SET last_active_at=now() WHERE id=$1`, id)
	return err
}

// GetSession returns a single session by ID, or nil if not found.
func (d *DB) GetSession(ctx context.Context, id string) (*Session, error) {
	const q = `
		SELECT id, COALESCE(user_id,''), persona, source, title, created_at, last_active_at,
		       last_input_tokens, total_output_tokens
		FROM agentd_sessions WHERE id=$1`
	var s Session
	err := d.pool.QueryRow(ctx, q, id).Scan(
		&s.ID, &s.UserID, &s.Persona, &s.Source, &s.Title,
		&s.CreatedAt, &s.LastActiveAt, &s.LastInputTokens, &s.TotalOutputTokens,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("agentdb: get session: %w", err)
	}
	return &s, nil
}

// DeleteSession removes a session and its messages (CASCADE).
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM agentd_sessions WHERE id=$1`, id)
	return err
}

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// UpdateTitle sets the generated title for a session.
func (d *DB) UpdateTitle(ctx context.Context, id, title string) error {
	_, err := d.pool.Exec(ctx, `UPDATE agentd_sessions SET title=$2 WHERE id=$1`, id, title)
	return err
}

// SearchSessions returns sessions whose title or message content matches the
// full-text query q, ordered by relevance then recency.
//
// userID filtering follows the same rules as ListSessions: empty = no filter
// (service accounts), non-empty = own sessions + anonymous sessions.
func (d *DB) SearchSessions(ctx context.Context, userID, q string) ([]Session, error) {
	// Build the tsquery from the raw input. plainto_tsquery handles arbitrary
	// user text safely (no injection risk, no syntax errors on odd input).
	const qAll = `
		SELECT DISTINCT ON (s.id)
		       s.id, COALESCE(s.user_id,''), s.persona, s.source, s.title,
		       s.created_at, s.last_active_at, s.last_input_tokens, s.total_output_tokens
		FROM agentd_sessions s
		LEFT JOIN agentd_messages m ON m.session_id = s.id
		WHERE plainto_tsquery('english', $1) @@ to_tsvector('english', s.title)
		   OR plainto_tsquery('english', $1) @@ to_tsvector('english', m.content)
		ORDER BY s.id, s.last_active_at DESC`
	const qUser = `
		SELECT DISTINCT ON (s.id)
		       s.id, COALESCE(s.user_id,''), s.persona, s.source, s.title,
		       s.created_at, s.last_active_at, s.last_input_tokens, s.total_output_tokens
		FROM agentd_sessions s
		LEFT JOIN agentd_messages m ON m.session_id = s.id
		WHERE (s.user_id = $2 OR s.user_id IS NULL)
		  AND (plainto_tsquery('english', $1) @@ to_tsvector('english', s.title)
		       OR plainto_tsquery('english', $1) @@ to_tsvector('english', m.content))
		ORDER BY s.id, s.last_active_at DESC`

	if userID == "" {
		r, err := d.pool.Query(ctx, qAll, q)
		if err != nil {
			return nil, fmt.Errorf("agentdb: search sessions: %w", err)
		}
		defer r.Close()
		return d.finishSearch(r)
	}
	r, err := d.pool.Query(ctx, qUser, q, userID)
	if err != nil {
		return nil, fmt.Errorf("agentdb: search sessions: %w", err)
	}
	defer r.Close()
	return d.finishSearch(r)
}

// finishSearch scans rows and re-sorts by last_active_at DESC.
// DISTINCT ON forces the primary ORDER BY to be on the partition key (s.id),
// so we do a final insertion sort here.
func (d *DB) finishSearch(rows pgxRows) ([]Session, error) {
	results, err := scanSessions(rows)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].LastActiveAt.After(results[j-1].LastActiveAt); j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
	return results, nil
}

func scanSessions(rows pgxRows) ([]Session, error) {
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.Persona, &s.Source, &s.Title,
			&s.CreatedAt, &s.LastActiveAt, &s.LastInputTokens, &s.TotalOutputTokens); err != nil {
			return nil, fmt.Errorf("agentdb: scan session: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
