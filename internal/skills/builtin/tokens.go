package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/compaction"
	"github.com/stanier/stoa/internal/skills"
)

// TokensCommand counts tokens in the current context
type TokensCommand struct{}

// NewTokensCommand creates a new tokens command
func NewTokensCommand() *TokensCommand {
	return &TokensCommand{}
}

// Name returns the command name
func (c *TokensCommand) Name() string {
	return "tokens"
}

// Description returns a human-readable description
func (c *TokensCommand) Description() string {
	return "Count tokens in current context"
}

// Usage returns usage instructions
func (c *TokensCommand) Usage() string {
	return "/tokens [--by-role]"
}

// Execute runs the command
func (c *TokensCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	// Remote mode: ask agentd for the authoritative server-side count.
	// This includes the system prompt and all tool messages the TUI never sees.
	if state.AgentClient != nil && state.SessionID != "" {
		resp, err := state.AgentClient.GetTokens(ctx, state.SessionID)
		if err != nil {
			return skills.CommandResult{
				Success: false,
				Message: fmt.Sprintf("Failed to get token counts from agentd: %v", err),
			}, nil
		}
		return skills.CommandResult{
			Success: true,
			Message: agentproto.FormatTokensResponse(resp),
		}, nil
	}

	// Standalone mode: count locally.
	if state.Config == nil {
		return skills.CommandResult{
			Success: false,
			Message: "No configuration available",
		}, nil
	}

	tokenCounter, err := compaction.NewTikTokenCounter(func() string { if dp := state.Config.LLM.DefaultProvider(); dp != nil { return dp.Model }; return "" }())
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create token counter: %v", err),
		}, nil
	}

	// Check if --by-role flag is provided
	byRole := false
	for _, arg := range args {
		if arg == "--by-role" {
			byRole = true
			break
		}
	}

	totalTokens, err := tokenCounter.CountMessages(state.Messages)
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to count tokens: %v", err),
		}, nil
	}

	message := fmt.Sprintf("Total tokens: %d\nMessages: %d", totalTokens, len(state.Messages))

	if byRole {
		roleCounts := make(map[chat.Role]int)
		roleTokens := make(map[chat.Role]int)
		for _, msg := range state.Messages {
			roleCounts[msg.Role]++
			count, _ := tokenCounter.Count(msg.Content)
			roleTokens[msg.Role] += count
		}
		message += "\n\nBreakdown by role:"
		for _, role := range []chat.Role{chat.RoleUser, chat.RoleAssistant, chat.RoleSystem, chat.RoleSummary} {
			if count, ok := roleCounts[role]; ok && count > 0 {
				message += fmt.Sprintf("\n  %s: %d tokens (%d messages)", role, roleTokens[role], count)
			}
		}
	}

	return skills.CommandResult{
		Success: true,
		Message: message,
	}, nil
}
