package agentdb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/agents"
)

// SeedAgentsFromPersonas upserts a published agent for each persona that does
// not already have a registry entry. Existing entries (matched by name) are
// left unchanged. Returns the number of agents created.
func (d *DB) SeedAgentsFromPersonas(ctx context.Context, ps []agents.Persona) (int, error) {
	if len(ps) == 0 {
		return 0, nil
	}

	emptyJSON, _ := json.Marshal([]string{})
	metaJSON := json.RawMessage("{}")

	created := 0
	for _, p := range ps {
		allowedTools := p.AvailableTools()
		toolsJSON, err := json.Marshal(allowedTools)
		if err != nil {
			return created, fmt.Errorf("agentdb: seed: marshal tools for %q: %w", p.Name(), err)
		}

		const q = `
			INSERT INTO agentd_agents
			    (id, name, display_name, description, system_prompt,
			     allowed_tools, mcp_servers, model_overrides,
			     visibility, owner_user_id, owner_team,
			     input_content_types, output_content_types, metadata,
			     published_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,'',$10,$11,$12,now())
			ON CONFLICT (name) DO NOTHING`

		tag, err := d.pool.Exec(ctx, q,
			uuid.New().String(),
			p.Name(),
			p.Name(),
			p.Description(),
			p.SystemPrompt(),
			string(toolsJSON),
			string(emptyJSON),
			string(metaJSON),
			string(AgentVisibilityInternal),
			string(emptyJSON),
			string(emptyJSON),
			string(metaJSON),
		)
		if err != nil {
			return created, fmt.Errorf("agentdb: seed agent %q: %w", p.Name(), err)
		}
		created += int(tag.RowsAffected())
	}
	return created, nil
}
