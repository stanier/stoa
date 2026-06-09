package agentproto

import "context"

// ClientAgentController implements skills.AgentController backed by an agentproto.Client.
// Used in stoa (embedded agentd) and remote modes where sub-agents live inside agentd.
type ClientAgentController struct {
	client    Client
	sessionID string
}

// NewClientAgentController returns a controller scoped to the given session.
func NewClientAgentController(client Client, sessionID string) *ClientAgentController {
	return &ClientAgentController{client: client, sessionID: sessionID}
}

func (c *ClientAgentController) ListSubAgents(ctx context.Context) ([]SubAgentInfo, error) {
	return c.client.ListSubAgents(ctx, c.sessionID)
}

func (c *ClientAgentController) SpawnSubAgent(ctx context.Context, persona, task string) (string, error) {
	return c.client.SpawnSubAgent(ctx, c.sessionID, SpawnSubAgentConfig{
		Persona: persona,
		Task:    task,
	})
}

func (c *ClientAgentController) KillSubAgent(ctx context.Context, agentID string) error {
	return c.client.KillSubAgent(ctx, c.sessionID, agentID)
}

func (c *ClientAgentController) BranchSession(ctx context.Context, persona, model string) (string, error) {
	resp, err := c.client.BranchSession(ctx, c.sessionID, BranchSessionConfig{
		Persona: persona,
		Model:   model,
	})
	if err != nil {
		return "", err
	}
	return resp.SessionID, nil
}
