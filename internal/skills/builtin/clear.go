package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/skills"
)

// ClearCommand clears the conversation history
type ClearCommand struct{}

// NewClearCommand creates a new clear command
func NewClearCommand() *ClearCommand {
	return &ClearCommand{}
}

// Name returns the command name
func (c *ClearCommand) Name() string {
	return "clear"
}

// Description returns a human-readable description
func (c *ClearCommand) Description() string {
	return "Clear all messages from the conversation"
}

// Usage returns usage instructions
func (c *ClearCommand) Usage() string {
	return "/clear"
}

// Execute runs the command
func (c *ClearCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	// Preserve the assistant system prompt (always the first system-role message).
	// It contains tool definitions that were injected after MCP connected and must
	// not be discarded just because the conversation history is cleared.
	var preserved []chat.Message
	for _, msg := range state.Messages {
		if msg.Role == chat.RoleSystem {
			preserved = append(preserved, msg)
			break
		}
	}
	state.Messages = preserved

	msg := "Conversation cleared."
	if state.TraceFilePath != "" {
		msg += fmt.Sprintf("\n\n**Trace**: `%s`\nTo resume: `chat --resume %s`", state.TraceFilePath, state.TraceFilePath)
	}

	return skills.CommandResult{
		Success:      true,
		Message:      msg,
		UpdatedState: state,
	}, nil
}
