package builtin

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/skills"
)

// KillCommand terminates an agent
type KillCommand struct {
	manager *agents.Manager
}

// NewKillCommand creates a new kill command
func NewKillCommand(manager *agents.Manager) *KillCommand {
	return &KillCommand{manager: manager}
}

// Name returns the command name
func (c *KillCommand) Name() string {
	return "kill"
}

// Description returns the command description
func (c *KillCommand) Description() string {
	return "Terminate an agent by ID or name"
}

// Usage returns usage instructions
func (c *KillCommand) Usage() string {
	return `/kill <agent-id or name>

Example: /kill Coder-12345678`
}

// Execute runs the command
func (c *KillCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	if len(args) < 1 {
		return skills.CommandResult{
			Success: false,
			Message: c.Usage(),
		}, nil
	}

	identifier := args[0]

	// Try to find agent by ID or name
	var agentID string
	var agentName string
	for _, agent := range c.manager.ListAgents() {
		if agent.ID() == identifier || agent.Name() == identifier {
			agentID = agent.ID()
			agentName = agent.Name()
			break
		}
	}

	if agentID == "" {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Agent not found: %s\n\nUse /agents to see active agents", identifier),
		}, nil
	}

	if err := c.manager.Kill(agentID); err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to kill agent: %v", err),
		}, err
	}

	return skills.CommandResult{
		Success: true,
		Message: fmt.Sprintf("✓ Killed agent: %s", agentName),
	}, nil
}
