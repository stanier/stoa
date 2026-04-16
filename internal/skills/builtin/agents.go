package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/skills"
)

// AgentsCommand lists all active agents
type AgentsCommand struct {
	manager *agents.Manager
}

// NewAgentsCommand creates a new agents command
func NewAgentsCommand(manager *agents.Manager) *AgentsCommand {
	return &AgentsCommand{manager: manager}
}

// Name returns the command name
func (c *AgentsCommand) Name() string {
	return "agents"
}

// Description returns the command description
func (c *AgentsCommand) Description() string {
	return "List all active agents and their status"
}

// Usage returns usage instructions
func (c *AgentsCommand) Usage() string {
	return `/agents [list|show|hide|toggle]

Subcommands:
  list   - List all active agents (default)
  show   - Show agent panel
  hide   - Hide agent panel
  toggle - Toggle panel visibility`
}

// Execute runs the command
func (c *AgentsCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	// Parse subcommand
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}

	switch subcommand {
	case "show":
		return skills.CommandResult{
			Success: true,
			Message: "",
			Metadata: map[string]interface{}{
				"ui_action":     "toggle_panel",
				"panel_visible": true,
			},
		}, nil

	case "hide":
		return skills.CommandResult{
			Success: true,
			Message: "",
			Metadata: map[string]interface{}{
				"ui_action":     "toggle_panel",
				"panel_visible": false,
			},
		}, nil

	case "toggle":
		return skills.CommandResult{
			Success: true,
			Message: "",
			Metadata: map[string]interface{}{
				"ui_action":     "toggle_panel",
				"panel_visible": "toggle",
			},
		}, nil

	case "list", "":
		return c.listAgents()

	default:
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Unknown subcommand: %s\n\n%s", subcommand, c.Usage()),
		}, nil
	}
}

// listAgents returns a list of all active agents
func (c *AgentsCommand) listAgents() (skills.CommandResult, error) {
	agentsList := c.manager.ListAgents()

	if len(agentsList) == 0 {
		return skills.CommandResult{
			Success: true,
			Message: "No active agents",
		}, nil
	}

	var b strings.Builder
	b.WriteString("Active Agents:\n\n")

	for _, agent := range agentsList {
		status := agent.Status()
		statusEmoji := getStatusEmoji(status)

		b.WriteString(fmt.Sprintf("%s %s (%s) - %s [%.0f%%]\n",
			statusEmoji,
			agent.Name(),
			agent.Persona().Name(),
			status,
			agent.Progress()*100))

		if task := agent.CurrentTask(); task != nil {
			b.WriteString(fmt.Sprintf("   Task: %s\n", task.Description))
		}
		b.WriteString("\n")
	}

	return skills.CommandResult{
		Success: true,
		Message: b.String(),
	}, nil
}

func getStatusEmoji(status agents.Status) string {
	switch status {
	case agents.StatusRunning:
		return "▶️"
	case agents.StatusCompleted:
		return "✅"
	case agents.StatusError:
		return "❌"
	case agents.StatusPaused:
		return "⏸️"
	case agents.StatusKilled:
		return "🛑"
	default:
		return "⏹️"
	}
}
