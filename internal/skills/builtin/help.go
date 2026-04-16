package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// HelpCommand displays available commands
type HelpCommand struct {
	registry interface {
		ListCommands() []skills.Command
	}
	prefix string
}

// NewHelpCommandWithPrefix creates a new help command with a custom prefix
func NewHelpCommandWithPrefix(registry interface{ ListCommands() []skills.Command }, prefix string) *HelpCommand {
	return &HelpCommand{
		registry: registry,
		prefix:   prefix,
	}
}

// Name returns the command name
func (c *HelpCommand) Name() string {
	return "help"
}

// Description returns a human-readable description
func (c *HelpCommand) Description() string {
	return "Display available commands"
}

// Usage returns usage instructions
func (c *HelpCommand) Usage() string {
	return c.prefix + "help [command]"
}

// Execute runs the command
func (c *HelpCommand) Execute(ctx context.Context, args []string, state *skills.CommandState) (skills.CommandResult, error) {
	var message strings.Builder

	if len(args) > 0 {
		// Show help for specific command
		message.WriteString(fmt.Sprintf("Help for command: %s\n(Command-specific help not yet implemented)", args[0]))
	} else {
		// List all commands
		message.WriteString("Available Commands:\n\n")

		commands := c.registry.ListCommands()
		for _, cmd := range commands {
			message.WriteString(fmt.Sprintf("%s%s - %s\n", c.prefix, cmd.Name(), cmd.Description()))
			message.WriteString(fmt.Sprintf("  Usage: %s\n\n", cmd.Usage()))
		}

		if c.prefix == "/" {
			message.WriteString("\nKeyboard Shortcuts:\n")
			message.WriteString("  Enter       Send message\n")
			message.WriteString("  Ctrl+C      Quit application\n")
			message.WriteString("  Ctrl+H      Toggle help screen\n")
			message.WriteString("  Arrow Keys  Scroll chat history\n")
		}
	}

	return skills.CommandResult{
		Success: true,
		Message: message.String(),
	}, nil
}
