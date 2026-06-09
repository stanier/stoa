package skills

import (
	"context"

	"github.com/stanier/stoa/internal/agentproto"
)

// AgentController is the interface the agent management commands (/spawn, /agents, /kill,
// /persona, /model) use to interact with live sub-agents and session branching.
// It is backed by either a local agents.Manager (standalone mode) or an
// agentproto.Client + session ID (stoa / remote mode).
type AgentController interface {
	ListSubAgents(ctx context.Context) ([]agentproto.SubAgentInfo, error)
	SpawnSubAgent(ctx context.Context, persona, task string) (string, error)
	KillSubAgent(ctx context.Context, agentID string) error
	// BranchSession forks the current session with an optional new persona and/or model.
	// Returns the new session ID.
	BranchSession(ctx context.Context, persona, model string) (string, error)
}
