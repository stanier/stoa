package agents

import (
	"github.com/stanier/stoa/internal/prompts"
)

// Persona interface defines agent personality and behavior
type Persona interface {
	// Identity
	Name() string
	Description() string

	// Configuration
	SystemPrompt() string
	SystemPromptTemplate() string // Returns raw template before rendering
	RenderSystemPrompt(data *prompts.TemplateData) (string, error)
	Capabilities() []string
	AvailableTools() []string

	// Behavior
	MaxConcurrentTasks() int
	ThinkingStyle() ThinkingStyle
	CommunicationPreference() CommunicationPreference
}

// PersonaRegistry defines the interface for persona discovery and retrieval
type PersonaRegistry interface {
	GetPersona(name string) (Persona, bool)
	ListPersonas() []Persona
}

// ThinkingStyle defines how an agent thinks and reasons
type ThinkingStyle string

const (
	ThinkingStyleFast     ThinkingStyle = "fast"     // Quick responses
	ThinkingStyleDeep     ThinkingStyle = "deep"     // Extended reasoning
	ThinkingStyleCreative ThinkingStyle = "creative" // High temperature
	ThinkingStylePrecise  ThinkingStyle = "precise"  // Low temperature
)

// CommunicationPreference defines how an agent communicates
type CommunicationPreference string

const (
	CommunicationVerbose CommunicationPreference = "verbose"
	CommunicationTerse   CommunicationPreference = "terse"
	CommunicationSilent  CommunicationPreference = "silent" // Only final result
)

// BasePersona is the default implementation of Persona interface
type BasePersona struct {
	name                    string
	description             string
	systemPromptTemplate    string // Raw template with {{.Variable}} placeholders
	systemPrompt            string // Cached rendered prompt
	capabilities            []string
	availableTools          []string
	maxConcurrentTasks      int
	thinkingStyle           ThinkingStyle
	communicationPreference CommunicationPreference
}

// NewBasePersona creates a base persona
func NewBasePersona(
	name, description, systemPrompt string,
	capabilities []string,
	availableTools []string,
	maxConcurrentTasks int,
	thinkingStyle ThinkingStyle,
	communicationPreference CommunicationPreference,
) *BasePersona {
	return &BasePersona{
		name:                    name,
		description:             description,
		systemPromptTemplate:    systemPrompt, // Store as template
		systemPrompt:            systemPrompt, // Initial cache (unrendered)
		capabilities:            capabilities,
		availableTools:          availableTools,
		maxConcurrentTasks:      maxConcurrentTasks,
		thinkingStyle:           thinkingStyle,
		communicationPreference: communicationPreference,
	}
}

// Name returns the persona name
func (p *BasePersona) Name() string {
	return p.name
}

// Description returns the persona description
func (p *BasePersona) Description() string {
	return p.description
}

// SystemPrompt returns the persona's system prompt (cached or unrendered)
func (p *BasePersona) SystemPrompt() string {
	return p.systemPrompt
}

// SystemPromptTemplate returns the raw template before rendering
func (p *BasePersona) SystemPromptTemplate() string {
	return p.systemPromptTemplate
}

// RenderSystemPrompt renders the system prompt template with given data.
// The result is not cached to avoid a data race when concurrent sub-agents
// call this with different template data.
func (p *BasePersona) RenderSystemPrompt(data *prompts.TemplateData) (string, error) {
	return prompts.RenderTemplate(p.systemPromptTemplate, data)
}

// Capabilities returns the persona's capabilities
func (p *BasePersona) Capabilities() []string {
	return p.capabilities
}

// AvailableTools returns the tools available to this persona
func (p *BasePersona) AvailableTools() []string {
	return p.availableTools
}

// MaxConcurrentTasks returns the maximum concurrent tasks for this persona
func (p *BasePersona) MaxConcurrentTasks() int {
	return p.maxConcurrentTasks
}

// ThinkingStyle returns the persona's thinking style
func (p *BasePersona) ThinkingStyle() ThinkingStyle {
	return p.thinkingStyle
}

// CommunicationPreference returns the persona's communication preference
func (p *BasePersona) CommunicationPreference() CommunicationPreference {
	return p.communicationPreference
}
