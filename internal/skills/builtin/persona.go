package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// PersonaCommand branches the current session with a new persona.
type PersonaCommand struct{}

func NewPersonaCommand() *PersonaCommand { return &PersonaCommand{} }

func (c *PersonaCommand) Name() string        { return "persona" }
func (c *PersonaCommand) Description() string { return "Branch to a new session with a different persona" }

func (c *PersonaCommand) Usage() string {
	return `/persona <name>

Forks the current session at the current message, applying the named persona's
system prompt and tool set to the new session.

Example: /persona coder`
}

func (c *PersonaCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	if state.AgentController == nil {
		return skills.CommandResult{Success: false, Message: "Session branching not available (not yet connected)"}, nil
	}
	if len(args) < 1 {
		return skills.CommandResult{Success: false, Message: c.Usage()}, nil
	}

	persona := strings.TrimSpace(args[0])
	newSessionID, err := state.AgentController.BranchSession(ctx, persona, "")
	if err != nil {
		return skills.CommandResult{
			Success: false,
			Message: fmt.Sprintf("Failed to branch session: %v", err),
		}, nil
	}

	return skills.CommandResult{
		Success: true,
		Message: fmt.Sprintf("Branched to new session with persona %q", persona),
		Metadata: map[string]interface{}{
			"ui_action":  "branch_session",
			"session_id": newSessionID,
			"persona":    persona,
		},
	}, nil
}
