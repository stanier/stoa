package agentdb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// AgentVisibility controls who can discover and invoke an agent.
type AgentVisibility string

const (
	AgentVisibilityInternal AgentVisibility = "internal"
	AgentVisibilityExternal AgentVisibility = "external"
)

// Agent is a row from agentd_agents.
type Agent struct {
	ID                 string
	Name               string
	DisplayName        string
	Description        string
	SystemPrompt       string
	AllowedTools       []string
	MCPServers         []string
	ModelOverrides     json.RawMessage
	Visibility         AgentVisibility
	OwnerUserID        string
	OwnerTeam          string
	InputContentTypes  []string
	OutputContentTypes []string
	Metadata           json.RawMessage
	ExternalURL        string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	PublishedAt        *time.Time
}

const agentSelectCols = `
	SELECT id, name, display_name, description, system_prompt,
	       allowed_tools::text, mcp_servers::text, model_overrides::text,
	       visibility, COALESCE(owner_user_id,''), owner_team,
	       input_content_types::text, output_content_types::text, metadata::text,
	       external_url,
	       created_at, updated_at, published_at
	FROM agentd_agents`

// CreateAgent inserts a new agent record.
func (d *DB) CreateAgent(ctx context.Context, a Agent) error {
	allowedTools, err := json.Marshal(a.AllowedTools)
	if err != nil {
		return fmt.Errorf("agentdb: marshal allowed_tools: %w", err)
	}
	mcpServers, err := json.Marshal(a.MCPServers)
	if err != nil {
		return fmt.Errorf("agentdb: marshal mcp_servers: %w", err)
	}
	modelOverrides := a.ModelOverrides
	if modelOverrides == nil {
		modelOverrides = json.RawMessage("{}")
	}
	inputCT, err := json.Marshal(a.InputContentTypes)
	if err != nil {
		return fmt.Errorf("agentdb: marshal input_content_types: %w", err)
	}
	outputCT, err := json.Marshal(a.OutputContentTypes)
	if err != nil {
		return fmt.Errorf("agentdb: marshal output_content_types: %w", err)
	}
	metadata := a.Metadata
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	const q = `
		INSERT INTO agentd_agents
		    (id, name, display_name, description, system_prompt,
		     allowed_tools, mcp_servers, model_overrides,
		     visibility, owner_user_id, owner_team,
		     input_content_types, output_content_types, metadata, external_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15)`
	_, err = d.pool.Exec(ctx, q,
		a.ID, a.Name, a.DisplayName, a.Description, a.SystemPrompt,
		string(allowedTools), string(mcpServers), string(modelOverrides),
		string(a.Visibility), a.OwnerUserID, a.OwnerTeam,
		string(inputCT), string(outputCT), string(metadata), a.ExternalURL,
	)
	if err != nil {
		return fmt.Errorf("agentdb: create agent: %w", err)
	}
	return nil
}

// GetAgent returns the agent with the given name.
func (d *DB) GetAgent(ctx context.Context, name string) (*Agent, error) {
	q := agentSelectCols + ` WHERE name=$1`
	row := d.pool.QueryRow(ctx, q, name)
	a, err := scanAgent(row)
	if err != nil {
		return nil, fmt.Errorf("agentdb: get agent: %w", err)
	}
	return a, nil
}

// ListAgents returns agents, optionally filtered by visibility and published status.
// visibility="" means all visibility values; publishedOnly=true filters to published_at IS NOT NULL.
func (d *DB) ListAgents(ctx context.Context, visibility string, publishedOnly bool) ([]Agent, error) {
	q := agentSelectCols
	var args []any
	clauses := []string{}

	if visibility != "" {
		args = append(args, visibility)
		clauses = append(clauses, fmt.Sprintf("visibility=$%d", len(args)))
	}
	if publishedOnly {
		clauses = append(clauses, "published_at IS NOT NULL")
	}
	if len(clauses) > 0 {
		q += " WHERE "
		for i, c := range clauses {
			if i > 0 {
				q += " AND "
			}
			q += c
		}
	}
	q += " ORDER BY created_at DESC"

	rows, err := d.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("agentdb: list agents: %w", err)
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("agentdb: scan agent: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// UpdateAgent replaces all mutable fields of the agent identified by name.
func (d *DB) UpdateAgent(ctx context.Context, name string, a Agent) error {
	allowedTools, err := json.Marshal(a.AllowedTools)
	if err != nil {
		return fmt.Errorf("agentdb: marshal allowed_tools: %w", err)
	}
	mcpServers, err := json.Marshal(a.MCPServers)
	if err != nil {
		return fmt.Errorf("agentdb: marshal mcp_servers: %w", err)
	}
	modelOverrides := a.ModelOverrides
	if modelOverrides == nil {
		modelOverrides = json.RawMessage("{}")
	}
	inputCT, err := json.Marshal(a.InputContentTypes)
	if err != nil {
		return fmt.Errorf("agentdb: marshal input_content_types: %w", err)
	}
	outputCT, err := json.Marshal(a.OutputContentTypes)
	if err != nil {
		return fmt.Errorf("agentdb: marshal output_content_types: %w", err)
	}
	metadata := a.Metadata
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}

	const q = `
		UPDATE agentd_agents SET
		    display_name=$2, description=$3, system_prompt=$4,
		    allowed_tools=$5, mcp_servers=$6, model_overrides=$7,
		    visibility=$8, owner_user_id=NULLIF($9,''), owner_team=$10,
		    input_content_types=$11, output_content_types=$12, metadata=$13,
		    external_url=$14,
		    updated_at=now()
		WHERE name=$1`
	_, err = d.pool.Exec(ctx, q,
		name,
		a.DisplayName, a.Description, a.SystemPrompt,
		string(allowedTools), string(mcpServers), string(modelOverrides),
		string(a.Visibility), a.OwnerUserID, a.OwnerTeam,
		string(inputCT), string(outputCT), string(metadata), a.ExternalURL,
	)
	if err != nil {
		return fmt.Errorf("agentdb: update agent: %w", err)
	}
	return nil
}

// DeleteAgent removes the agent with the given name.
func (d *DB) DeleteAgent(ctx context.Context, name string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM agentd_agents WHERE name=$1`, name)
	if err != nil {
		return fmt.Errorf("agentdb: delete agent: %w", err)
	}
	return nil
}

// PublishAgent sets published_at to now() for the agent with the given name.
func (d *DB) PublishAgent(ctx context.Context, name string) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE agentd_agents SET published_at=now(), updated_at=now() WHERE name=$1`, name)
	if err != nil {
		return fmt.Errorf("agentdb: publish agent: %w", err)
	}
	return nil
}

// UnpublishAgent clears published_at for the agent with the given name.
func (d *DB) UnpublishAgent(ctx context.Context, name string) error {
	_, err := d.pool.Exec(ctx,
		`UPDATE agentd_agents SET published_at=NULL, updated_at=now() WHERE name=$1`, name)
	if err != nil {
		return fmt.Errorf("agentdb: unpublish agent: %w", err)
	}
	return nil
}

// pgxScanner is satisfied by both pgx.Row and pgx.Rows.
type pgxScanner interface {
	Scan(dest ...any) error
}

func scanAgent(s pgxScanner) (*Agent, error) {
	var (
		a                                                     Agent
		allowedToolsRaw, mcpServersRaw, modelOverridesRaw    []byte
		inputCTRaw, outputCTRaw, metadataRaw                 []byte
	)
	err := s.Scan(
		&a.ID, &a.Name, &a.DisplayName, &a.Description, &a.SystemPrompt,
		&allowedToolsRaw, &mcpServersRaw, &modelOverridesRaw,
		&a.Visibility, &a.OwnerUserID, &a.OwnerTeam,
		&inputCTRaw, &outputCTRaw, &metadataRaw,
		&a.ExternalURL,
		&a.CreatedAt, &a.UpdatedAt, &a.PublishedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(allowedToolsRaw, &a.AllowedTools); err != nil {
		return nil, fmt.Errorf("unmarshal allowed_tools: %w", err)
	}
	if err := json.Unmarshal(mcpServersRaw, &a.MCPServers); err != nil {
		return nil, fmt.Errorf("unmarshal mcp_servers: %w", err)
	}
	a.ModelOverrides = modelOverridesRaw
	if err := json.Unmarshal(inputCTRaw, &a.InputContentTypes); err != nil {
		return nil, fmt.Errorf("unmarshal input_content_types: %w", err)
	}
	if err := json.Unmarshal(outputCTRaw, &a.OutputContentTypes); err != nil {
		return nil, fmt.Errorf("unmarshal output_content_types: %w", err)
	}
	a.Metadata = metadataRaw
	return &a, nil
}
