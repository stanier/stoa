package agentdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/stanier/stoa/internal/chat"
)

// InsertMessages inserts messages starting at seqStart using ON CONFLICT DO NOTHING
// so it is safe to call with overlapping ranges (idempotent).
func (d *DB) InsertMessages(ctx context.Context, sessionID string, msgs []chat.Message, seqStart int) error {
	if len(msgs) == 0 {
		return nil
	}
	const q = `
		INSERT INTO agentd_messages
		  (session_id, seq, role, content, thinking, tool_name, tool_use_id, tool_success)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (session_id, seq) DO NOTHING`
	batch := &pgx.Batch{}
	for i, m := range msgs {
		batch.Queue(q,
			sessionID, seqStart+i,
			string(m.Role), m.Content, m.Thinking,
			m.ToolName, m.ToolUseID, m.ToolSuccess,
		)
	}
	br := d.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := range msgs {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("agentdb: insert message seq %d: %w", seqStart+i, err)
		}
	}
	return br.Close()
}

// GetMessages returns all non-system messages for a session in seq order.
func (d *DB) GetMessages(ctx context.Context, sessionID string) ([]chat.Message, error) {
	const q = `
		SELECT role, content, thinking, tool_name, tool_use_id, tool_success
		FROM agentd_messages
		WHERE session_id=$1
		ORDER BY seq ASC`
	rows, err := d.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agentdb: get messages: %w", err)
	}
	defer rows.Close()

	var msgs []chat.Message
	for rows.Next() {
		var m chat.Message
		var role string
		if err := rows.Scan(&role, &m.Content, &m.Thinking, &m.ToolName, &m.ToolUseID, &m.ToolSuccess); err != nil {
			return nil, fmt.Errorf("agentdb: scan message: %w", err)
		}
		m.Role = chat.Role(role)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteMessages removes all messages for a session. Used when clearing a session.
func (d *DB) DeleteMessages(ctx context.Context, sessionID string) error {
	_, err := d.pool.Exec(ctx,
		`DELETE FROM agentd_messages WHERE session_id=$1`, sessionID)
	if err != nil {
		return fmt.Errorf("agentdb: delete messages: %w", err)
	}
	return nil
}

// MessageCount returns the number of persisted messages for a session.
func (d *DB) MessageCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agentd_messages WHERE session_id=$1`, sessionID,
	).Scan(&count)
	return count, err
}
