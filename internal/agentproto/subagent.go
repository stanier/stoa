package agentproto

// SubAgentInfo is a snapshot of a live sub-agent running within a session.
type SubAgentInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Persona string `json:"persona"`
	Task    string `json:"task"`
	Status  string `json:"status"`
}

// SpawnSubAgentConfig is the request body for spawning a new sub-agent.
type SpawnSubAgentConfig struct {
	Persona string `json:"persona"`
	Task    string `json:"task"`
}
