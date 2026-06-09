package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/skills"
)

// KillCommand cancels a running sub-agent by ID or name.
type KillCommand struct{}

// NewKillCommand creates a new kill command.
func NewKillCommand() *KillCommand {
	return &KillCommand{}
}

func (c *KillCommand) Name() string        { return "kill" }
func (c *KillCommand) Description() string { return "Cancel a running sub-agent by ID or name" }

func (c *KillCommand) Usage() string {
	return `/kill <agent-id>

Example: /kill 550e8400-e29b-41d4-a716-446655440000`
}

func (c *KillCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	if state.AgentController == nil {
		return skills.CommandResult{Success: false, Message: "Agent management not available (not yet connected)"}, nil
	}
	if len(args) < 1 {
		return skills.CommandResult{Success: false, Message: c.Usage()}, nil
	}

	identifier := args[0]

	// Resolve name → ID if needed.
	agentID := identifier
	agents, err := state.AgentController.ListSubAgents(ctx)
	if err != nil {
		return skills.CommandResult{Success: false, Message: fmt.Sprintf("Failed to list agents: %v", err)}, err
	}
	for _, a := range agents {
		if a.Name == identifier {
			agentID = a.ID
			break
		}
	}

	if err := state.AgentController.KillSubAgent(ctx, agentID); err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to kill agent %s: %v\n\nUse /agents to see active agents", identifier, err),
		}, err
	}

	return skills.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Cancelled sub-agent: %s", agentID),
	}, nil
}
