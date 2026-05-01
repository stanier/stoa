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
		dp := state.Config.LLM.DefaultProvider()
		if dp == nil {
			message = "No LLM provider configured"
		} else {
		temp := 1.0
		if dp.Temperature != nil {
			temp = *dp.Temperature
		}

		message = fmt.Sprintf("Current Configuration:\n"+
			"  Provider: %s (%s)\n"+
			"  Model: %s\n"+
			"  API URL: %s\n"+
			"  Max Tokens: %d\n"+
			"  Temperature: %.1f",
			dp.Name,
			dp.Type,
			dp.Model,
			dp.APIURL,
			dp.MaxTokens,
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
		} // end else dp != nil
	} else {
		message = "No configuration available"
	}

	return skills.CommandResult{
		Success: true,
		Message: message,
	}, nil
}
