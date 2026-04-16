package builtin

import (
	"context"
	"encoding/json"

	"github.com/stanier/stoa/internal/skills"
)

// SubAgentStubTool is a placeholder that makes sub-agent tools visible in the
// LLM's tool list and system prompt. Real implementations are registered
// per-session by agentd via perSessionTools (which executeToolCall checks first),
// so the stub's Execute is only reached outside an agentd session.
type SubAgentStubTool struct {
	name        string
	description string
	schema      skills.ToolSchema
}

// NewSubAgentStubTool creates a stub tool with the given name, description, and schema.
func NewSubAgentStubTool(name, description string, schema skills.ToolSchema) *SubAgentStubTool {
	return &SubAgentStubTool{name: name, description: description, schema: schema}
}

func (t *SubAgentStubTool) Name() string             { return t.name }
func (t *SubAgentStubTool) Description() string       { return t.description }
func (t *SubAgentStubTool) Schema() skills.ToolSchema { return t.schema }

func (t *SubAgentStubTool) Execute(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	return skills.ToolResult{
		Success: false,
		Error:   t.name + " requires an active agentd session",
	}, nil
}
