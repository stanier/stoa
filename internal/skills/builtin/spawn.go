package builtin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/skills"
)

// SpawnCommand spawns a new agent with a specific persona
type SpawnCommand struct {
	manager *agents.Manager
}

// NewSpawnCommand creates a new spawn command
func NewSpawnCommand(manager *agents.Manager) *SpawnCommand {
	return &SpawnCommand{manager: manager}
}

// Name returns the command name
func (c *SpawnCommand) Name() string {
	return "spawn"
}

// Description returns the command description
func (c *SpawnCommand) Description() string {
	return "Spawn a new agent with a specific persona to work on a task"
}

// Usage returns usage instructions
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

// Execute runs the command
func (c *SpawnCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	if len(args) < 2 {
		return skills.CommandResult{
			Success: false,
			Message: c.Usage(),
		}, nil
	}

	personaName := args[0]
	taskDesc := strings.Join(args[1:], " ")

	registry := c.manager.PersonaRegistry()
	if registry == nil {
		return skills.CommandResult{
			Success: false,
			Message: "Persona registry not initialized",
		}, nil
	}

	persona, ok := registry.GetPersona(personaName)
	if !ok {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Unknown persona: %s\n\n%s", personaName, c.Usage()),
		}, nil
	}

	agent, err := c.manager.Spawn(persona, "")
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to spawn agent: %v", err),
		}, err
	}

	task := agents.Task{
		ID:          generateTaskID(),
		Description: taskDesc,
		Priority:    agents.PriorityNormal,
	}

	_, err = c.manager.Delegate(ctx, agent.ID(), task)
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to delegate task: %v", err),
		}, err
	}

	return skills.CommandResult{
		Success: true,
		Message: fmt.Sprintf("✓ Spawned %s agent: %s\nTask: %s", persona.Name(), agent.Name(), taskDesc),
		Metadata: map[string]interface{}{
			"is_spawn":  true,
			"ui_action": "show_panel_on_first_spawn",
		},
	}, nil
}

func generateTaskID() string {
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}
