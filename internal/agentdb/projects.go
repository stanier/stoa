package agentdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProjectFile is a metadata record for a file attached to a project.
type ProjectFile struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// Project is a row from agentd_projects.
type Project struct {
	ID           string
	UserID       string
	Name         string
	Description  string
	Color        string
	Glyph        string
	Emoji        string
	Instructions string
	DefaultModel string
	DefaultTools []string
	Files        []ProjectFile
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const projectSelectCols = `
	SELECT id, user_id, name, description, color, glyph, emoji, instructions,
	       default_model, default_tools::text, files::text, created_at, updated_at
	FROM agentd_projects`

// CreateProject inserts a new project record.
func (d *DB) CreateProject(ctx context.Context, p Project) error {
	tools, err := json.Marshal(p.DefaultTools)
	if err != nil {
		return fmt.Errorf("agentdb: marshal default_tools: %w", err)
	}
	files := p.Files
	if files == nil {
		files = []ProjectFile{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("agentdb: marshal files: %w", err)
	}
	const q = `
		INSERT INTO agentd_projects
		    (id, user_id, name, description, color, glyph, emoji, instructions,
		     default_model, default_tools, files)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err = d.pool.Exec(ctx, q,
		p.ID, p.UserID, p.Name, p.Description, p.Color, p.Glyph, p.Emoji, p.Instructions,
		p.DefaultModel, string(tools), string(filesJSON),
	)
	if err != nil {
		return fmt.Errorf("agentdb: create project: %w", err)
	}
	return nil
}

// GetProject returns the project with the given id.
// When userID is non-empty, the query also filters on user_id (ownership check).
// Returns nil, nil when not found.
func (d *DB) GetProject(ctx context.Context, id, userID string) (*Project, error) {
	q := projectSelectCols + ` WHERE id=$1`
	args := []any{id}
	if userID != "" {
		q += ` AND user_id=$2`
		args = append(args, userID)
	}
	row := d.pool.QueryRow(ctx, q, args...)
	p, err := scanProject(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("agentdb: get project: %w", err)
	}
	return p, nil
}

// ListProjects returns all projects ordered by created_at descending.
// When userID is non-empty, returns only that user's projects.
func (d *DB) ListProjects(ctx context.Context, userID string) ([]Project, error) {
	q := projectSelectCols
	var args []any
	if userID != "" {
		q += ` WHERE user_id=$1`
		args = append(args, userID)
	}
	q += ` ORDER BY created_at DESC`

	rows, err := d.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("agentdb: list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("agentdb: scan project: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateProject replaces all mutable fields of the project identified by id.
// When userID is non-empty, the update is limited to rows owned by that user.
func (d *DB) UpdateProject(ctx context.Context, p Project) error {
	tools := p.DefaultTools
	if tools == nil {
		tools = []string{}
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return fmt.Errorf("agentdb: marshal default_tools: %w", err)
	}
	files := p.Files
	if files == nil {
		files = []ProjectFile{}
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("agentdb: marshal files: %w", err)
	}

	const q = `
		UPDATE agentd_projects SET
		    name=$2, description=$3, color=$4, glyph=$5, emoji=$6, instructions=$7,
		    default_model=$8, default_tools=$9, files=$10,
		    updated_at=now()
		WHERE id=$1 AND ($11='' OR user_id=$11)`
	tag, err := d.pool.Exec(ctx, q,
		p.ID, p.Name, p.Description, p.Color, p.Glyph, p.Emoji, p.Instructions,
		p.DefaultModel, string(toolsJSON), string(filesJSON),
		p.UserID,
	)
	if err != nil {
		return fmt.Errorf("agentdb: update project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteProject removes the project with the given id.
// Sessions that belonged to it have their project_id set to NULL by the FK cascade.
// When userID is non-empty, deletion is limited to rows owned by that user.
func (d *DB) DeleteProject(ctx context.Context, id, userID string) error {
	const q = `DELETE FROM agentd_projects WHERE id=$1 AND ($2='' OR user_id=$2)`
	tag, err := d.pool.Exec(ctx, q, id, userID)
	if err != nil {
		return fmt.Errorf("agentdb: delete project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MoveSession updates the project_id of a session.
// When projectID is empty, project_id is set to NULL (session becomes loose).
// When projectID is non-empty, the target project must exist and belong to userID
// (unless userID is empty, meaning service-account bypass).
// Returns pgx.ErrNoRows if the session doesn't exist, is owned by a different user,
// or the target project doesn't belong to userID.
func (d *DB) MoveSession(ctx context.Context, sessionID, projectID, userID string) error {
	var err error
	if projectID == "" {
		// Remove from any project — just verify session ownership.
		const q = `
			UPDATE agentd_sessions
			SET project_id = NULL
			WHERE id=$1 AND ($2='' OR user_id=$2 OR user_id IS NULL)`
		var tag interface{ RowsAffected() int64 }
		tag, err = d.pool.Exec(ctx, q, sessionID, userID)
		if err != nil {
			return fmt.Errorf("agentdb: move session (clear): %w", err)
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}

	// Assign to a project — verify that the target project belongs to userID.
	const q = `
		UPDATE agentd_sessions s
		SET project_id = $2
		WHERE s.id = $1
		  AND ($3='' OR s.user_id=$3 OR s.user_id IS NULL)
		  AND EXISTS (
		      SELECT 1 FROM agentd_projects p
		      WHERE p.id=$2 AND ($3='' OR p.user_id=$3)
		  )`
	tag, err := d.pool.Exec(ctx, q, sessionID, projectID, userID)
	if err != nil {
		return fmt.Errorf("agentdb: move session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// pgxScanner is satisfied by both pgx.Row and pgx.Rows.
// (Duplicated from agents.go to keep the file self-contained; the compiler deduplicates.)
type pgxProjectScanner interface {
	Scan(dest ...any) error
}

func scanProject(s pgxProjectScanner) (*Project, error) {
	var (
		p             Project
		toolsRaw      []byte
		filesRaw      []byte
	)
	err := s.Scan(
		&p.ID, &p.UserID, &p.Name, &p.Description, &p.Color, &p.Glyph, &p.Emoji,
		&p.Instructions, &p.DefaultModel, &toolsRaw, &filesRaw,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(toolsRaw, &p.DefaultTools); err != nil {
		return nil, fmt.Errorf("unmarshal default_tools: %w", err)
	}
	if err := json.Unmarshal(filesRaw, &p.Files); err != nil {
		return nil, fmt.Errorf("unmarshal files: %w", err)
	}
	if p.DefaultTools == nil {
		p.DefaultTools = []string{}
	}
	if p.Files == nil {
		p.Files = []ProjectFile{}
	}
	return &p, nil
}
