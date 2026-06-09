package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// ModelCommand displays current model info, or branches to a new session with a different model.
type ModelCommand struct{}

func NewModelCommand() *ModelCommand { return &ModelCommand{} }

func (c *ModelCommand) Name() string        { return "model" }
func (c *ModelCommand) Description() string { return "Show current model, or branch to a new session with a different model" }

func (c *ModelCommand) Usage() string {
	return `/model [<model-id>]

Without arguments: display current provider and model configuration.
With a model ID: branch the current session using the specified model.

Example: /model claude-opus-4-7`
}

func (c *ModelCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	// With an argument: branch to a new session with the given model.
	if len(args) > 0 {
		model := strings.TrimSpace(args[0])
		if state.AgentController == nil {
			return skills.CommandResult{Success: false, Message: "Session branching not available (not yet connected)"}, nil
		}
		newSessionID, err := state.AgentController.BranchSession(ctx, "", model)
		if err != nil {
			return skills.CommandResult{
				Success: false,
				Message: fmt.Sprintf("Failed to branch session: %v", err),
			}, nil
		}
		return skills.CommandResult{
			Success: true,
			Message: fmt.Sprintf("Branched to new session with model %q", model),
			Metadata: map[string]interface{}{
				"ui_action":  "branch_session",
				"session_id": newSessionID,
				"model":      model,
			},
		}, nil
	}

	// Without arguments: display current configuration.
	if state.Config == nil {
		return skills.CommandResult{Success: true, Message: "No configuration available"}, nil
	}

	dp := state.Config.LLM.DefaultProvider()
	if dp == nil {
		return skills.CommandResult{Success: true, Message: "No LLM provider configured"}, nil
	}

	temp := 1.0
	if dp.Temperature != nil {
		temp = *dp.Temperature
	}
	msg := fmt.Sprintf("Current Configuration:\n"+
		"  Provider: %s (%s)\n"+
		"  Model: %s\n"+
		"  API URL: %s\n"+
		"  Max Tokens: %d\n"+
		"  Temperature: %.1f",
		dp.Name, dp.Type, dp.Model, dp.APIURL, dp.MaxTokens, temp,
	)
	if state.Config.Compaction.Enabled {
		msg += fmt.Sprintf("\n\nCompaction:\n"+
			"  Enabled: true\n"+
			"  Threshold: %d tokens\n"+
			"  Strategy: %s\n"+
			"  Keep Recent: %d messages",
			state.Config.Compaction.CompactThreshold,
			state.Config.Compaction.Strategy,
			state.Config.Compaction.KeepRecent,
		)
	}
	return skills.CommandResult{Success: true, Message: msg}, nil
}
