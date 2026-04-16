package agentd

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/skills"
)

// listToolsTool is a per-session tool that lets the LLM discover what tools are
// available. The LLM is only told about this tool up front; calling it reveals
// the full tool set for subsequent iterations.
type listToolsTool struct {
	mu     *sync.Mutex
	llmCtx *app.LLMContext // pointer so updates to sess.llmCtx are reflected
}

func newListToolsTool(mu *sync.Mutex, llmCtx *app.LLMContext) *listToolsTool {
	return &listToolsTool{mu: mu, llmCtx: llmCtx}
}

func (t *listToolsTool) Name() string { return "list_tools" }

func (t *listToolsTool) Description() string {
	return "List all tools available in this session. Call this first to discover what you can do before taking action."
}

func (t *listToolsTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type:       "object",
		Properties: map[string]interface{}{},
		Required:   []string{},
	}
}

// toolEntry is the JSON representation of a single tool returned by list_tools.
type toolEntry struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Schema      interface{} `json:"schema,omitempty"`
}

func (t *listToolsTool) Execute(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	t.mu.Lock()
	tools := make([]llm.Tool, len(t.llmCtx.Tools))
	copy(tools, t.llmCtx.Tools)
	t.mu.Unlock()

	entries := make([]toolEntry, 0, len(tools))
	for _, tool := range tools {
		entries = append(entries, toolEntry{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
		})
	}

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return skills.ToolResult{Success: false, Error: "failed to marshal tool list"}, nil
	}
	return skills.ToolResult{Success: true, Content: string(out)}, nil
}
