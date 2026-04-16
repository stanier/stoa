package skills

import (
	"context"
	"encoding/json"
	"testing"
)

// Mock command for testing
type mockCommand struct {
	name string
}

func (c *mockCommand) Name() string        { return c.name }
func (c *mockCommand) Description() string { return "Mock command" }
func (c *mockCommand) Usage() string       { return "/mock" }
func (c *mockCommand) Execute(ctx context.Context, args []string, state *CommandState) (CommandResult, error) {
	return CommandResult{Success: true, Message: "mock executed"}, nil
}

// Mock tool for testing
type mockTool struct {
	name string
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return "Mock tool" }
func (t *mockTool) Schema() ToolSchema {
	return ToolSchema{Type: "object"}
}
func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (ToolResult, error) {
	return ToolResult{Success: true, Content: "mock result"}, nil
}

func TestRegistry_RegisterCommand(t *testing.T) {
	r := NewRegistry()

	cmd := &mockCommand{name: "test"}
	r.RegisterCommand(cmd)

	retrieved, ok := r.GetCommand("test")
	if !ok {
		t.Error("Command not found after registration")
	}

	if retrieved.Name() != "test" {
		t.Errorf("Expected command name 'test', got '%s'", retrieved.Name())
	}
}

func TestRegistry_RegisterTool(t *testing.T) {
	r := NewRegistry()

	tool := &mockTool{name: "test"}
	r.RegisterTool(tool)

	retrieved, ok := r.GetTool("test")
	if !ok {
		t.Error("Tool not found after registration")
	}

	if retrieved.Name() != "test" {
		t.Errorf("Expected tool name 'test', got '%s'", retrieved.Name())
	}
}

func TestRegistry_ListCommands(t *testing.T) {
	r := NewRegistry()

	r.RegisterCommand(&mockCommand{name: "cmd1"})
	r.RegisterCommand(&mockCommand{name: "cmd2"})

	commands := r.ListCommands()
	if len(commands) != 2 {
		t.Errorf("Expected 2 commands, got %d", len(commands))
	}
}

func TestRegistry_ListTools(t *testing.T) {
	r := NewRegistry()

	r.RegisterTool(&mockTool{name: "tool1"})
	r.RegisterTool(&mockTool{name: "tool2"})

	tools := r.ListTools()
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(tools))
	}
}

func TestRegistry_UnregisterCommand(t *testing.T) {
	r := NewRegistry()

	r.RegisterCommand(&mockCommand{name: "test"})

	err := r.UnregisterCommand("test")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	_, ok := r.GetCommand("test")
	if ok {
		t.Error("Command should not exist after unregistration")
	}

	// Try to unregister non-existent command
	err = r.UnregisterCommand("nonexistent")
	if err == nil {
		t.Error("Expected error when unregistering non-existent command")
	}
}

func TestRegistry_UnregisterTool(t *testing.T) {
	r := NewRegistry()

	r.RegisterTool(&mockTool{name: "test"})

	err := r.UnregisterTool("test")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	_, ok := r.GetTool("test")
	if ok {
		t.Error("Tool should not exist after unregistration")
	}

	// Try to unregister non-existent tool
	err = r.UnregisterTool("nonexistent")
	if err == nil {
		t.Error("Expected error when unregistering non-existent tool")
	}
}
