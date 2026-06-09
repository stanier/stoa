package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// AgentsCommand lists active sub-agents and controls the agent panel.
type AgentsCommand struct{}

// NewAgentsCommand creates a new agents command.
func NewAgentsCommand() *AgentsCommand {
	return &AgentsCommand{}
}

func (c *AgentsCommand) Name() string        { return "agents" }
func (c *AgentsCommand) Description() string { return "List active sub-agents and their status" }

func (c *AgentsCommand) Usage() string {
	return `/agents [list|show|hide|toggle]

Subcommands:
  list   - List all active sub-agents (default)
  show   - Show agent panel
  hide   - Hide agent panel
  toggle - Toggle panel visibility`
}

func (c *AgentsCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}

	switch subcommand {
	case "show":
		return skills.CommandResult{
			Success:  true,
			Metadata: map[string]interface{}{"ui_action": "toggle_panel", "panel_visible": true},
		}, nil

	case "hide":
		return skills.CommandResult{
			Success:  true,
			Metadata: map[string]interface{}{"ui_action": "toggle_panel", "panel_visible": false},
		}, nil

	case "toggle":
		return skills.CommandResult{
			Success:  true,
			Metadata: map[string]interface{}{"ui_action": "toggle_panel", "panel_visible": "toggle"},
		}, nil

	case "list", "":
		if state.AgentController == nil {
			return skills.CommandResult{Success: true, Message: "No active sub-agents (agent management not connected)"}, nil
		}
		agents, err := state.AgentController.ListSubAgents(ctx)
		if err != nil {
			return skills.CommandResult{Success: false, Message: fmt.Sprintf("Failed to list agents: %v", err)}, err
		}
		if len(agents) == 0 {
			return skills.CommandResult{Success: true, Message: "No active sub-agents"}, nil
		}
		var b strings.Builder
		b.WriteString("Active sub-agents:\n\n")
		for _, a := range agents {
			name := a.Name
			if name == "" {
				name = a.ID
			}
			b.WriteString(fmt.Sprintf("%s %s (%s) — %s\n", statusGlyph(a.Status), name, a.Persona, a.Status))
			if a.Task != "" {
				b.WriteString(fmt.Sprintf("   Task: %s\n", a.Task))
			}
			b.WriteString("\n")
		}
		return skills.CommandResult{Success: true, Message: b.String()}, nil

	default:
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Unknown subcommand: %s\n\n%s", subcommand, c.Usage()),
		}, nil
	}
}

func statusGlyph(status string) string {
	switch status {
	case "running":
		return "▶"
	case "completed":
		return "✓"
	case "error":
		return "✗"
	case "waiting":
		return "⏸"
	case "cancelled", "killed":
		return "■"
	default:
		return "○"
	}
}
