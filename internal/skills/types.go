package skills

import (
	"context"
	"encoding/json"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
)

// Command represents a user-invocable slash command
type Command interface {
	// Name returns the command name (without slash prefix)
	Name() string

	// Description returns a human-readable description
	Description() string

	// Usage returns usage instructions
	Usage() string

	// Execute runs the command with given arguments
	Execute(ctx context.Context, args []string, state *CommandState) (CommandResult, error)
}

// Tool represents an LLM-accessible tool
type Tool interface {
	// Name returns the tool name
	Name() string

	// Description returns a human-readable description
	Description() string

	// Schema returns the JSON Schema for the tool parameters
	Schema() ToolSchema

	// Execute runs the tool with given parameters
	Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

// CommandState provides context for command execution
type CommandState struct {
	Messages      []chat.Message
	Config        *config.Config
	LLMClient     llm.Client
	TraceFilePath string // Path to the current trace file (empty if tracing disabled or N/A)

	// Remote mode fields — both nil/empty in standalone mode.
	// When AgentClient is non-nil, the TUI is connected to agentd and commands
	// should prefer server-side data over the local message snapshot.
	AgentClient agentproto.Client // nil in standalone mode
	SessionID   string            // empty in standalone mode
}

// CommandResult represents the result of a command execution
type CommandResult struct {
	Success      bool
	Message      string
	UpdatedState *CommandState          // Optional updated state
	Metadata     map[string]interface{} // Optional metadata for UI actions
}

// ToolSchema represents JSON Schema for tool parameters
type ToolSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	Success bool
	Content string
	Error   string
}
