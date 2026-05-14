package skills

import (
	"fmt"
	"sync"
)

// Registry manages commands and tools
type Registry struct {
	commands map[string]Command
	tools    map[string]Tool
	mu       sync.RWMutex
}

// NewRegistry creates a new registry
func NewRegistry() *Registry {
	r := &Registry{
		commands: make(map[string]Command),
		tools:    make(map[string]Tool),
	}

	return r
}

// RegisterCommand adds a command to the registry
func (r *Registry) RegisterCommand(cmd Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[cmd.Name()] = cmd
}

// RegisterTool adds a tool to the registry
func (r *Registry) RegisterTool(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

// GetCommand retrieves a command by name
func (r *Registry) GetCommand(name string) (Command, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.commands[name]
	return cmd, ok
}

// GetTool retrieves a tool by name
func (r *Registry) GetTool(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// ListCommands returns all registered commands
func (r *Registry) ListCommands() []Command {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	commands := make([]Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		commands = append(commands, cmd)
	}
	return commands
}

// ListTools returns all registered tools
func (r *Registry) ListTools() []Tool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// UnregisterCommand removes a command from the registry
func (r *Registry) UnregisterCommand(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.commands[name]; !ok {
		return fmt.Errorf("command not found: %s", name)
	}

	delete(r.commands, name)
	return nil
}

// UnregisterTool removes a tool from the registry
func (r *Registry) UnregisterTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tools[name]; !ok {
		return fmt.Errorf("tool not found: %s", name)
	}

	delete(r.tools, name)
	return nil
}
