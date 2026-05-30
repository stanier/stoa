package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
)

// ManagerController implements skills.AgentController backed by a local Manager.
// Used in standalone chat mode where all sub-agent state lives in the TUI process.
type ManagerController struct {
	manager *Manager
}

// NewManagerController wraps manager in a skills.AgentController.
func NewManagerController(manager *Manager) *ManagerController {
	return &ManagerController{manager: manager}
}

func (c *ManagerController) ListSubAgents(ctx context.Context) ([]agentproto.SubAgentInfo, error) {
	var result []agentproto.SubAgentInfo
	for _, a := range c.manager.ListAgents() {
		task := ""
		if t := a.CurrentTask(); t != nil {
			task = t.Description
		}
		result = append(result, agentproto.SubAgentInfo{
			ID:      a.ID(),
			Name:    a.Name(),
			Persona: a.Persona().Name(),
			Task:    task,
			Status:  string(a.Status()),
		})
	}
	return result, nil
}

func (c *ManagerController) SpawnSubAgent(ctx context.Context, persona, task string) (string, error) {
	registry := c.manager.PersonaRegistry()
	if registry == nil {
		return "", fmt.Errorf("persona registry not initialized")
	}
	p, ok := registry.GetPersona(persona)
	if !ok {
		return "", fmt.Errorf("unknown persona: %s", persona)
	}
	agent, err := c.manager.Spawn(p, "")
	if err != nil {
		return "", fmt.Errorf("spawn agent: %w", err)
	}
	t := Task{
		ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Description: task,
		Priority:    PriorityNormal,
	}
	if _, err := c.manager.Delegate(ctx, agent.ID(), t); err != nil {
		return "", fmt.Errorf("delegate task: %w", err)
	}
	return agent.ID(), nil
}

func (c *ManagerController) KillSubAgent(ctx context.Context, agentID string) error {
	return c.manager.Kill(agentID)
}

// BranchSession is not supported in standalone manager mode — no agentd to fork into.
func (c *ManagerController) BranchSession(_ context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("session branching requires agentd (not available in standalone mode)")
}
