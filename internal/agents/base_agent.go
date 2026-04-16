package agents

import (
	"fmt"

	"github.com/stanier/stoa/internal/chat"
)

// AgentOptions for configuring agent behavior
type AgentOptions struct {
	MaxRetries   int
	Timeout      int
	TraceEnabled bool
}

// AgentView provides read-only access to agent state
type AgentView struct {
	ID       string
	Persona  string
	Name     string
	Status   Status
	Progress float64
	Task     string
}

// NewAgentView creates a view of the agent state
func (a *BaseAgent) NewAgentView() *AgentView {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var taskDesc string
	if a.currentTask != nil {
		taskDesc = a.currentTask.Description
	}

	return &AgentView{
		ID:       a.id,
		Persona:  a.persona.Name(),
		Name:     a.name,
		Status:   a.status,
		Progress: a.progress,
		Task:     taskDesc,
	}
}

// MarshalState returns a JSON-serializable state snapshot
func (a *BaseAgent) MarshalState() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]interface{}{
		"id":               a.id,
		"persona":          a.persona.Name(),
		"name":             a.name,
		"status":           string(a.status),
		"progress":         a.progress,
		"current_task":     a.currentTask,
		"conversation_len": len(a.conversation),
	}
}

// Reset clears the agent's state for reuse
func (a *BaseAgent) Reset() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status == StatusRunning {
		return newAgentError(a.id, "cannot reset running agent")
	}

	a.status = StatusIdle
	a.currentTask = nil
	a.progress = 0.0

	// Use getSystemPrompt for consistent behavior with new/existing agents
	systemPrompt := a.persona.SystemPrompt()
	if a.promptRegistry != nil {
		personaName := a.persona.Name()
		if rendered, err := a.promptRegistry.Render(personaName, nil); err == nil {
			systemPrompt = rendered
		}
	}

	a.conversation = []chat.Message{
		chat.NewSystemMessage(systemPrompt),
	}
	return nil
}

// String returns a string representation of the agent
func (a *BaseAgent) String() string {
	return fmt.Sprintf("Agent{id=%s, name=%s, persona=%s, status=%s, progress=%.0f%%}",
		a.id, a.name, a.persona.Name(), a.status, a.progress*100)
}

// GetID returns the agent ID (alias for ID() for convenience)
func (a *BaseAgent) GetID() string {
	return a.id
}

// GetPersona returns the agent persona (alias for Persona())
func (a *BaseAgent) GetPersona() Persona {
	return a.persona
}

// GetName returns the agent name (alias for Name())
func (a *BaseAgent) GetName() string {
	return a.name
}
