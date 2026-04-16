package agents

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// Manager manages multiple agents and their lifecycle
type Manager struct {
	agents          map[string]Agent
	sharedContext   *SharedContext
	messageBus      *MessageBus
	llmClient       llm.Client
	skillRegistry   *skills.Registry
	promptRegistry  *prompts.Registry
	personaRegistry PersonaRegistry
	config          *config.Config
	maxConcurrent   int
	mainAgentID     string // ID of the primary chat agent
	mu              sync.RWMutex
}

// NewManager creates a new agent manager
func NewManager(llmClient llm.Client, skillRegistry *skills.Registry, maxConcurrent int) *Manager {
	return NewManagerWithConfig(llmClient, skillRegistry, nil, nil, maxConcurrent)
}

// NewManagerWithConfig creates a new agent manager with config and prompt registry
func NewManagerWithConfig(llmClient llm.Client, skillRegistry *skills.Registry, promptRegistry *prompts.Registry, cfg *config.Config, maxConcurrent int) *Manager {
	return &Manager{
		agents:          make(map[string]Agent),
		sharedContext:   NewSharedContext(),
		messageBus:      NewMessageBus(),
		llmClient:       llmClient,
		skillRegistry:   skillRegistry,
		promptRegistry:  promptRegistry,
		personaRegistry: nil, // Should be set via SetPersonaRegistry
		config:          cfg,
		maxConcurrent:   maxConcurrent,
	}
}

// SetPersonaRegistry sets the persona registry for the manager
func (m *Manager) SetPersonaRegistry(r PersonaRegistry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.personaRegistry = r
}

// PersonaRegistry returns the persona registry
func (m *Manager) PersonaRegistry() PersonaRegistry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.personaRegistry
}

// NewManagerWithPrompts is a legacy constructor
func NewManagerWithPrompts(llmClient llm.Client, skillRegistry *skills.Registry, promptRegistry *prompts.Registry, maxConcurrent int) *Manager {
	return NewManagerWithConfig(llmClient, skillRegistry, promptRegistry, nil, maxConcurrent)
}

// Spawn creates and starts a new agent with the given persona
func (m *Manager) Spawn(persona Persona, name string) (Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.agents) >= m.maxConcurrent {
		return nil, fmt.Errorf("maximum concurrent agents (%d) reached", m.maxConcurrent)
	}

	id := generateID()
	if name == "" {
		name = fmt.Sprintf("%s-%s", persona.Name(), id[:8])
	}

	agent := NewBaseAgentWithPrompts(id, persona, name, m.llmClient, m.skillRegistry, m.promptRegistry)
	m.agents[id] = agent

	// Subscribe to agent messages in background
	go m.subscribeToAgent(agent)

	return agent, nil
}

// SpawnMain creates the primary chat agent
func (m *Manager) SpawnMain(persona Persona, name string) (Agent, error) {
	agent, err := m.Spawn(persona, name)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.mainAgentID = agent.ID()
	m.mu.Unlock()

	return agent, nil
}

// GetMainAgent retrieves the primary chat agent
func (m *Manager) GetMainAgent() (Agent, bool) {
	m.mu.RLock()
	id := m.mainAgentID
	m.mu.RUnlock()

	if id == "" {
		return nil, false
	}
	return m.GetAgent(id)
}

// subscribeToAgent listens for messages from an agent and publishes to bus
func (m *Manager) subscribeToAgent(agent Agent) {
	msgChan := agent.ReceiveMessages()
	for msg := range msgChan {
		m.messageBus.Publish(msg)
	}
}

// Kill terminates an agent by ID
func (m *Manager) Kill(agentID string) error {
	m.mu.Lock()
	agent, exists := m.agents[agentID]
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	if err := agent.Stop(); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.agents, agentID)
	m.mu.Unlock()

	return nil
}

// Pause pauses an agent by ID
func (m *Manager) Pause(agentID string) error {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	return agent.Pause()
}

// Resume resumes a paused agent by ID
func (m *Manager) Resume(agentID string) error {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	return agent.Resume()
}

// Delegate assigns a task to an agent
func (m *Manager) Delegate(ctx context.Context, agentID string, task Task) (<-chan Message, error) {
	m.mu.RLock()
	agent, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	return agent.Execute(ctx, task)
}

// GetAgent retrieves an agent by ID
func (m *Manager) GetAgent(agentID string) (Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agent, exists := m.agents[agentID]
	return agent, exists
}

// ListAgents returns all active agents
func (m *Manager) ListAgents() []Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agents := make([]Agent, 0, len(m.agents))
	for _, agent := range m.agents {
		agents = append(agents, agent)
	}

	// Sort agents by ID for stable, deterministic ordering
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID() < agents[j].ID()
	})

	return agents
}

// Subscribe returns a channel for receiving all agent messages and an
// unsubscribe function. Call the unsubscribe function when done to remove the
// subscriber and close the channel.
func (m *Manager) Subscribe() (<-chan Message, func()) {
	return m.messageBus.Subscribe()
}

// GetSharedContext returns the shared context
func (m *Manager) GetSharedContext() *SharedContext {
	return m.sharedContext
}

// Config returns the manager's configuration
func (m *Manager) Config() *config.Config {
	return m.config
}

// Shutdown gracefully stops all agents
func (m *Manager) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, agent := range m.agents {
		if err := agent.Stop(); err != nil {
			logging.Warnf("agent %s failed to stop cleanly: %v", id, err)
		}
		delete(m.agents, id)
	}

	m.messageBus.Close()
	return nil
}

// GetStats returns manager statistics
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agentsByStatus := make(map[string]int)
	for _, agent := range m.agents {
		status := string(agent.Status())
		agentsByStatus[status]++
	}

	stats := map[string]interface{}{
		"total_agents":     len(m.agents),
		"max_concurrent":   m.maxConcurrent,
		"agents_by_status": agentsByStatus,
	}

	return stats
}
