package agentdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DecisionGraph is a row from agentd_decision_graphs. Graph is the web
// editor's document, stored as given.
type DecisionGraph struct {
	ID        string
	UserID    string
	Name      string
	Graph     json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// The userID arguments below follow the projects convention: non-empty limits
// the query to that owner's rows, empty means no owner filter (service
// accounts, or agentd running without auth).

// CreateDecisionGraph inserts a graph.
func (d *DB) CreateDecisionGraph(ctx context.Context, g DecisionGraph) error {
	const q = `INSERT INTO agentd_decision_graphs (id, user_id, name, graph) VALUES ($1,$2,$3,$4)`
	if _, err := d.pool.Exec(ctx, q, g.ID, g.UserID, g.Name, string(g.Graph)); err != nil {
		return fmt.Errorf("agentdb: create decision graph: %w", err)
	}
	return nil
}

// GetDecisionGraph returns one graph, or nil, nil when there is none the
// caller may see.
func (d *DB) GetDecisionGraph(ctx context.Context, id, userID string) (*DecisionGraph, error) {
	const q = `SELECT id, user_id, name, graph::text, created_at, updated_at
		FROM agentd_decision_graphs WHERE id=$1 AND ($2='' OR user_id=$2)`
	var g DecisionGraph
	var graph string
	err := d.pool.QueryRow(ctx, q, id, userID).Scan(&g.ID, &g.UserID, &g.Name, &graph, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentdb: get decision graph: %w", err)
	}
	g.Graph = json.RawMessage(graph)
	return &g, nil
}

// ListDecisionGraphs returns graphs without their documents, most recently
// updated first.
func (d *DB) ListDecisionGraphs(ctx context.Context, userID string) ([]DecisionGraph, error) {
	const q = `SELECT id, user_id, name, created_at, updated_at
		FROM agentd_decision_graphs WHERE ($1='' OR user_id=$1) ORDER BY updated_at DESC, id`
	rows, err := d.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("agentdb: list decision graphs: %w", err)
	}
	defer rows.Close()
	var out []DecisionGraph
	for rows.Next() {
		var g DecisionGraph
		if err := rows.Scan(&g.ID, &g.UserID, &g.Name, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("agentdb: scan decision graph: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CountDecisionGraphs counts one owner's graphs.
func (d *DB) CountDecisionGraphs(ctx context.Context, userID string) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM agentd_decision_graphs WHERE user_id=$1`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("agentdb: count decision graphs: %w", err)
	}
	return n, nil
}

// UpdateDecisionGraph replaces a graph's name and document. It returns
// pgx.ErrNoRows when there is no such graph the caller may change.
func (d *DB) UpdateDecisionGraph(ctx context.Context, g DecisionGraph) error {
	const q = `UPDATE agentd_decision_graphs SET name=$2, graph=$3, updated_at=now()
		WHERE id=$1 AND ($4='' OR user_id=$4)`
	tag, err := d.pool.Exec(ctx, q, g.ID, g.Name, string(g.Graph), g.UserID)
	if err != nil {
		return fmt.Errorf("agentdb: update decision graph: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteDecisionGraph removes a graph. It returns pgx.ErrNoRows when there is
// no such graph the caller may delete.
func (d *DB) DeleteDecisionGraph(ctx context.Context, id, userID string) error {
	tag, err := d.pool.Exec(ctx, `DELETE FROM agentd_decision_graphs WHERE id=$1 AND ($2='' OR user_id=$2)`, id, userID)
	if err != nil {
		return fmt.Errorf("agentdb: delete decision graph: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
