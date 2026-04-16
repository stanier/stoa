package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/skills"
)

// ModelCommand displays current model information
type ModelCommand struct{}

// NewModelCommand creates a new model command
func NewModelCommand() *ModelCommand {
	return &ModelCommand{}
}

// Name returns the command name
func (c *ModelCommand) Name() string {
	return "model"
}

// Description returns a human-readable description
func (c *ModelCommand) Description() string {
	return "Display current model and provider information"
}

// Usage returns usage instructions
func (c *ModelCommand) Usage() string {
	return "/model"
}

// Execute runs the command
func (c *ModelCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	var message string

	if state.Config != nil {
		provider := state.Config.LLM.Provider
		if provider == "" {
			provider = "openai"
		}

		temp := 1.0
		if state.Config.LLM.Temperature != nil {
			temp = *state.Config.LLM.Temperature
		}

		message = fmt.Sprintf("Current Configuration:\n"+
			"  Provider: %s\n"+
			"  Model: %s\n"+
			"  API URL: %s\n"+
			"  Max Tokens: %d\n"+
			"  Temperature: %.1f",
			provider,
			state.Config.LLM.Model,
			state.Config.LLM.APIURL,
			state.Config.LLM.MaxTokens,
			temp,
		)

		// Add compaction info if enabled
		if state.Config.Compaction.Enabled {
			message += fmt.Sprintf("\n\nCompaction:\n"+
				"  Enabled: true\n"+
				"  Threshold: %d tokens\n"+
				"  Strategy: %s\n"+
				"  Keep Recent: %d messages",
				state.Config.Compaction.CompactThreshold,
				state.Config.Compaction.Strategy,
				state.Config.Compaction.KeepRecent,
			)
		}
	} else {
		message = "No configuration available"
	}

	return skills.CommandResult{
		Success: true,
		Message: message,
	}, nil
}
