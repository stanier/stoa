package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// SpawnCommand spawns a new sub-agent with the given persona and task.
type SpawnCommand struct{}

// NewSpawnCommand creates a new spawn command.
func NewSpawnCommand() *SpawnCommand {
	return &SpawnCommand{}
}

func (c *SpawnCommand) Name() string        { return "spawn" }
func (c *SpawnCommand) Description() string { return "Spawn a new sub-agent with a specific persona" }

func (c *SpawnCommand) Usage() string {
	return `/spawn <persona> <task description>

Personas:
  coder      - Code generation and debugging specialist
  analyzer   - Code analysis and review specialist
  researcher - Research and documentation specialist
  planner    - Task planning and architecture specialist
  default    - General-purpose conversational agent

Example: /spawn coder "Write a function to calculate fibonacci numbers"`
}

func (c *SpawnCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	if state.AgentController == nil {
		return skills.CommandResult{Success: false, Message: "Agent management not available (not yet connected)"}, nil
	}
	if len(args) < 2 {
		return skills.CommandResult{Success: false, Message: c.Usage()}, nil
	}

	persona := args[0]
	task := strings.Join(args[1:], " ")

	agentID, err := state.AgentController.SpawnSubAgent(ctx, persona, task)
	if err != nil {
		return skills.CommandResult{Success: false, Message: fmt.Sprintf("Failed to spawn agent: %v", err)}, err
	}

	return skills.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Spawned %s agent (id: %s)\nTask: %s", persona, agentID, task),
		Metadata: map[string]interface{}{
			"is_spawn":  true,
			"ui_action": "show_panel_on_first_spawn",
		},
	}, nil
}
