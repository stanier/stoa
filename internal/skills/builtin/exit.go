package builtin

import (
	"context"

	"github.com/stanier/stoa/internal/skills"
)

// ExitCommand quits the TUI.
type ExitCommand struct{}

func NewExitCommand() *ExitCommand { return &ExitCommand{} }

func (c *ExitCommand) Name() string        { return "exit" }
func (c *ExitCommand) Description() string { return "Quit stoa" }
func (c *ExitCommand) Usage() string       { return `/exit` }

func (c *ExitCommand) Execute(_ context.Context, _ []string, _ *skills.CommandState) (skills.CommandResult, error) {
	return skills.CommandResult{
		Success:  true,
		Metadata: map[string]interface{}{"ui_action": "quit"},
	}, nil
}
