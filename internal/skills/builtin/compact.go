package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/compaction"
	"github.com/stanier/stoa/internal/skills"
)

// CompactCommand manually triggers conversation compaction
type CompactCommand struct{}

// NewCompactCommand creates a new compact command
func NewCompactCommand() *CompactCommand {
	return &CompactCommand{}
}

// Name returns the command name
func (c *CompactCommand) Name() string {
	return "compact"
}

// Description returns a human-readable description
func (c *CompactCommand) Description() string {
	return "Manually trigger conversation compaction"
}

// Usage returns usage instructions
func (c *CompactCommand) Usage() string {
	return "/compact"
}

// Execute runs the command
func (c *CompactCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	// Check if compaction is enabled
	if state.Config == nil || !state.Config.Compaction.Enabled {
		return skills.CommandResult{
			Success: false,
			Message: "Compaction is not enabled in configuration",
		}, nil
	}

	// Check if we have an LLM client
	if state.LLMClient == nil {
		return skills.CommandResult{
			Success: false,
			Message: "LLM client not available for compaction",
		}, nil
	}

	// Create token counter
	tokenCounter, err := compaction.NewTikTokenCounter(func() string { if dp := state.Config.LLM.DefaultProvider(); dp != nil { return dp.Model }; return "" }())
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create token counter: %v", err),
		}, nil
	}

	// Count current tokens
	currentTokens, err := tokenCounter.CountMessages(state.Messages)
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to count tokens: %v", err),
		}, nil
	}

	// Check if there are enough messages to compact
	if len(state.Messages) <= state.Config.Compaction.KeepRecent {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Too few messages to compact (have %d, need more than %d)",
				len(state.Messages), state.Config.Compaction.KeepRecent),
		}, nil
	}

	// Create compactor
	var strategy compaction.Strategy
	switch state.Config.Compaction.Strategy {
	case "sliding_window":
		strategy = compaction.NewSlidingWindowStrategy(state.Config.Compaction.KeepRecent)
	case "importance":
		strategy = compaction.NewImportanceBasedStrategy(state.Config.Compaction.KeepRecent)
	default:
		strategy = compaction.NewSlidingWindowStrategy(state.Config.Compaction.KeepRecent)
	}

	compactor := compaction.NewDefaultCompactor(
		state.LLMClient,
		tokenCounter,
		strategy,
		state.Config.Compaction.MaxTokens,
		state.Config.Compaction.CompactThreshold,
	)

	// Perform compaction
	newMessages, err := compactor.Compact(ctx, state.Messages)
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Compaction failed: %v", err),
		}, nil
	}

	// Add summary message
	newMessages = append(newMessages, chat.NewSummaryMessage("Conversation manually compacted"))

	// Count new tokens
	newTokens, _ := tokenCounter.CountMessages(newMessages)

	// Update state
	state.Messages = newMessages

	message := fmt.Sprintf("Compaction complete!\n"+
		"  Before: %d tokens, %d messages\n"+
		"  After: %d tokens, %d messages\n"+
		"  Saved: %d tokens",
		currentTokens, len(state.Messages),
		newTokens, len(newMessages),
		currentTokens-newTokens,
	)

	return skills.CommandResult{
		Success:      true,
		Message:      message,
		UpdatedState: state,
	}, nil
}
