package agents

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// Agent interface defines the contract for all agent types
type Agent interface {
	// Identity
	ID() string
	Persona() Persona
	Name() string

	// Execution
	Execute(ctx context.Context, task Task) (<-chan Message, error)
	Stop() error
	Pause() error
	Resume() error

	// State
	Status() Status
	Progress() float64
	CurrentTask() *Task

	// Communication
	SendMessage(msg Message) error
	ReceiveMessages() <-chan Message
}

// Status represents the agent's current state
type Status string

const (
	StatusIdle      Status = "idle"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusError     Status = "error"
	StatusKilled    Status = "killed"
)

// Priority represents task priority levels
type Priority int

const (
	PriorityLow    Priority = 0
	PriorityNormal Priority = 1
	PriorityHigh   Priority = 2
	PriorityUrgent Priority = 3
)

// MessageType represents the type of message
type MessageType string

const (
	MessageTypeThinking MessageType = "thinking" // Reasoning progress
	MessageTypeProgress MessageType = "progress" // Task progress update
	MessageTypeResult   MessageType = "result"   // Final or intermediate result
	MessageTypeError    MessageType = "error"    // Error occurred
	MessageTypeQuestion MessageType = "question" // Needs user input
	MessageTypeToolUse  MessageType = "tool_use" // Using a tool
	MessageTypeSubtask  MessageType = "subtask"  // Created subtask
	MessageTypePing     MessageType = "ping"     // Inter-agent communication
)

// Task represents a unit of work for an agent
type Task struct {
	ID           string                 `json:"id"`
	Description  string                 `json:"description"`
	Priority     Priority               `json:"priority"`
	Context      map[string]interface{} `json:"context"`
	Deadline     *time.Time             `json:"deadline,omitempty"`
	ParentTaskID string                 `json:"parent_task_id,omitempty"`
}

// Message represents communication between agents and UI
type Message struct {
	ID        string                 `json:"id"`
	AgentID   string                 `json:"agent_id"`
	Type      MessageType            `json:"type"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// BaseAgent is the default implementation of the Agent interface
type BaseAgent struct {
	id          string
	persona     Persona
	name        string
	status      Status
	currentTask *Task
	progress    float64

	llmClient      llm.Client
	skillRegistry  *skills.Registry
	promptRegistry *prompts.Registry
	conversation   []chat.Message

	incomingMsgs chan Message
	outgoingMsgs chan Message
	stopChan     chan struct{}
	pauseChan    chan struct{}
	resumeChan   chan struct{}

	wg sync.WaitGroup
	mu sync.RWMutex
}

// NewBaseAgent creates a new agent instance
func NewBaseAgent(id string, persona Persona, name string, llmClient llm.Client, skillRegistry *skills.Registry) *BaseAgent {
	return NewBaseAgentWithPrompts(id, persona, name, llmClient, skillRegistry, nil)
}

// NewBaseAgentWithPrompts creates a new agent instance with prompt registry
func NewBaseAgentWithPrompts(id string, persona Persona, name string, llmClient llm.Client, skillRegistry *skills.Registry, promptRegistry *prompts.Registry) *BaseAgent {
	agent := &BaseAgent{
		id:             id,
		persona:        persona,
		name:           name,
		status:         StatusIdle,
		llmClient:      llmClient,
		skillRegistry:  skillRegistry,
		promptRegistry: promptRegistry,
		conversation:   []chat.Message{},
		incomingMsgs:   make(chan Message, 10),
		outgoingMsgs:   make(chan Message, 100),
		stopChan:       make(chan struct{}),
		pauseChan:      make(chan struct{}),
		resumeChan:     make(chan struct{}),
	}

	// Add system message with persona
	systemPrompt := agent.getSystemPrompt()
	agent.conversation = append(agent.conversation, chat.NewSystemMessage(systemPrompt))

	return agent
}

// getSystemPrompt returns the system prompt for this agent, using registry if available
func (a *BaseAgent) getSystemPrompt() string {
	// Try to load persona prompt from registry first
	if a.promptRegistry != nil && a.persona != nil {
		personaName := a.persona.Name()

		// Build template data for persona rendering
		templateData := prompts.BuildTemplateData(a.skillRegistry, "", "")

		// Try to render persona prompt from registry
		if rendered, err := a.promptRegistry.Render(personaName, templateData); err == nil {
			return rendered
		}
	}

	// Fallback to persona's hardcoded system prompt
	return a.persona.SystemPrompt()
}

// ID returns the agent's unique identifier
func (a *BaseAgent) ID() string {
	return a.id
}

// Persona returns the agent's persona
func (a *BaseAgent) Persona() Persona {
	return a.persona
}

// Name returns the agent's display name
func (a *BaseAgent) Name() string {
	return a.name
}

// GetConversation returns the current conversation history
func (a *BaseAgent) GetConversation() []chat.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy to avoid data races
	msgs := make([]chat.Message, len(a.conversation))
	copy(msgs, a.conversation)
	return msgs
}

// SetConversation sets the conversation history
func (a *BaseAgent) SetConversation(msgs []chat.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.conversation = msgs
}

// Status returns the current agent status
func (a *BaseAgent) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// Progress returns the current task progress (0.0 to 1.0)
func (a *BaseAgent) Progress() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.progress
}

// CurrentTask returns the currently executing task
func (a *BaseAgent) CurrentTask() *Task {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentTask
}

// Execute starts executing a task
func (a *BaseAgent) Execute(ctx context.Context, task Task) (<-chan Message, error) {
	a.mu.Lock()
	if a.status != StatusIdle && a.status != StatusCompleted {
		a.mu.Unlock()
		return nil, newAgentError(a.id, "agent is already executing a task")
	}

	a.status = StatusRunning
	a.currentTask = &task
	a.progress = 0.0
	a.mu.Unlock()

	// Start goroutines with proper lifecycle tracking
	a.wg.Add(2)

	// Start incoming message listener
	go a.listenForIncomingMessages(ctx)

	// Start execution in goroutine
	go a.executeTask(ctx, task)

	return a.outgoingMsgs, nil
}

// listenForIncomingMessages listens for user messages sent to the agent
func (a *BaseAgent) listenForIncomingMessages(ctx context.Context) {
	defer a.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopChan:
			return
		case msg := <-a.incomingMsgs:
			a.processIncomingMessage(ctx, msg)
		}
	}
}

// executeTask runs the actual task execution with tool support
func (a *BaseAgent) executeTask(ctx context.Context, task Task) {
	defer a.wg.Done()
	defer func() {
		a.mu.Lock()
		if a.status == StatusRunning {
			a.status = StatusCompleted
			a.progress = 1.0
		}
		a.mu.Unlock()
	}()

	// Send thinking message
	a.sendMessage(Message{
		ID:        generateID(),
		AgentID:   a.id,
		Type:      MessageTypeThinking,
		Content:   "Starting task: " + task.Description,
		Timestamp: time.Now(),
	})

	// Add task to conversation
	a.mu.Lock()
	a.conversation = append(a.conversation, chat.NewUserMessage(task.Description))
	a.mu.Unlock()

	for {
		// Get available tools
		tools := a.getAvailableTools()

		// Stream response from LLM
		streamChan, err := a.llmClient.StreamMessage(ctx, a.conversation, tools)
		if err != nil {
			a.sendMessage(Message{
				ID:        generateID(),
				AgentID:   a.id,
				Type:      MessageTypeError,
				Content:   err.Error(),
				Timestamp: time.Now(),
			})
			return
		}

		var response strings.Builder
		var lastToolUse *llm.ToolUse

		for chunk := range streamChan {
			select {
			case <-ctx.Done():
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeError,
					Content:   "task cancelled",
					Timestamp: time.Now(),
				})
				return
			case <-a.stopChan:
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeError,
					Content:   "task stopped",
					Timestamp: time.Now(),
				})
				return
			case <-a.pauseChan:
				<-a.resumeChan // Wait for resume
			default:
			}

			if chunk.Error != nil {
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeError,
					Content:   chunk.Error.Error(),
					Timestamp: time.Now(),
				})
				return
			}

			if chunk.Thinking != "" {
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeThinking,
					Content:   chunk.Thinking,
					Timestamp: time.Now(),
				})
			}

			if chunk.Content != "" {
				response.WriteString(chunk.Content)
				// Periodically update progress based on turns or content length?
				// For now keep it simple
			}

			if chunk.ToolUse != nil {
				lastToolUse = chunk.ToolUse
			}
		}

		// Add assistant response to conversation
		assistantMsg := response.String()
		a.mu.Lock()
		a.conversation = append(a.conversation, chat.NewAssistantMessage(assistantMsg))
		a.mu.Unlock()

		if lastToolUse != nil {
			// Execute tool
			a.sendMessage(Message{
				ID:        generateID(),
				AgentID:   a.id,
				Type:      MessageTypeToolUse,
				Content:   "Using tool: " + lastToolUse.Name,
				Metadata:  map[string]interface{}{"tool": lastToolUse.Name},
				Timestamp: time.Now(),
			})

			result, err := a.executeTool(ctx, *lastToolUse)
			if err != nil {
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeError,
					Content:   fmt.Sprintf("Tool %s failed: %v", lastToolUse.Name, err),
					Timestamp: time.Now(),
				})
				return
			}

			// Add tool result to conversation and loop
			a.mu.Lock()
			toolMsg := chat.NewToolResultMessage(lastToolUse.Name, lastToolUse.ID, result.Content, result.Success)
			a.conversation = append(a.conversation, toolMsg)
			a.mu.Unlock()

			// Loop to let LLM process tool result
			continue
		}

		// No more tool uses, we are done
		a.sendMessage(Message{
			ID:        generateID(),
			AgentID:   a.id,
			Type:      MessageTypeResult,
			Content:   assistantMsg,
			Timestamp: time.Now(),
		})
		break
	}
}

// executeTool performs tool execution via skill registry
func (a *BaseAgent) executeTool(ctx context.Context, toolUse llm.ToolUse) (skills.ToolResult, error) {
	tool, ok := a.skillRegistry.GetTool(toolUse.Name)
	if !ok {
		return skills.ToolResult{}, fmt.Errorf("unknown tool: %s", toolUse.Name)
	}

	return tool.Execute(ctx, toolUse.Parameters)
}

// Stop terminates the agent's current task
func (a *BaseAgent) Stop() error {
	a.mu.Lock()
	wasRunning := a.status == StatusRunning

	if a.status == StatusKilled || a.status == StatusCompleted {
		a.mu.Unlock()
		return nil // Already stopped
	}

	if wasRunning {
		close(a.stopChan)
	}

	a.status = StatusKilled
	a.mu.Unlock()

	// Wait for goroutines to finish (outside the lock to avoid deadlock)
	if wasRunning {
		a.wg.Wait()
	}

	return nil
}

// Pause pauses the agent's current task.
// Status is updated under the lock, then the signal is sent outside it so we
// don't block the mutex while waiting for the execution goroutine to receive.
func (a *BaseAgent) Pause() error {
	a.mu.Lock()
	if a.status != StatusRunning {
		a.mu.Unlock()
		return newAgentError(a.id, "agent is not running")
	}
	a.status = StatusPaused
	a.mu.Unlock()

	a.pauseChan <- struct{}{}
	return nil
}

// Resume resumes a paused agent.
// Status is updated under the lock, then the signal is sent outside it so we
// don't block the mutex while waiting for the execution goroutine to receive.
func (a *BaseAgent) Resume() error {
	a.mu.Lock()
	if a.status != StatusPaused {
		a.mu.Unlock()
		return newAgentError(a.id, "agent is not paused")
	}
	a.status = StatusRunning
	a.mu.Unlock()

	a.resumeChan <- struct{}{}
	return nil
}

// SendMessage sends a message to the agent
func (a *BaseAgent) SendMessage(msg Message) error {
	select {
	case a.incomingMsgs <- msg:
		return nil
	default:
		return newAgentError(a.id, "incoming message queue full")
	}
}

// ReceiveMessages returns the channel for receiving messages
func (a *BaseAgent) ReceiveMessages() <-chan Message {
	return a.outgoingMsgs
}

// sendMessage sends a message to the outgoing channel
func (a *BaseAgent) sendMessage(msg Message) {
	select {
	case a.outgoingMsgs <- msg:
	default:
		// Drop message if channel is full
	}
}

// processIncomingMessage handles a user message sent to the agent
func (a *BaseAgent) processIncomingMessage(ctx context.Context, msg Message) {
	if msg.Type == MessageTypeQuestion {
		// User sent a question/message - add to conversation
		a.mu.Lock()
		a.conversation = append(a.conversation, chat.NewUserMessage(msg.Content))
		a.mu.Unlock()

		// Send user message notification to UI
		a.sendMessage(Message{
			ID:        generateID(),
			AgentID:   a.id,
			Type:      MessageTypeProgress,
			Content:   fmt.Sprintf("User: %s", msg.Content),
			Timestamp: time.Now(),
		})

		// Get LLM response to user message
		a.respondToUserMessage(ctx)
	}
}

// respondToUserMessage gets an LLM response to a user message, supporting multi-turn tools.
// ctx should be the agent's execution context so the stream can be cancelled when the agent stops.
func (a *BaseAgent) respondToUserMessage(ctx context.Context) {
	for {
		// Get available tools
		tools := a.getAvailableTools()

		// Stream response from LLM
		streamChan, err := a.llmClient.StreamMessage(ctx, a.conversation, tools)
		if err != nil {
			a.sendMessage(Message{
				ID:        generateID(),
				AgentID:   a.id,
				Type:      MessageTypeError,
				Content:   fmt.Sprintf("Failed to get response: %v", err),
				Timestamp: time.Now(),
			})
			return
		}

		var response strings.Builder
		var lastToolUse *llm.ToolUse

		for chunk := range streamChan {
			// Accumulate response content
			if chunk.Content != "" {
				response.WriteString(chunk.Content)

				// Stream partial response to UI
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeProgress,
					Content:   chunk.Content,
					Timestamp: time.Now(),
				})
			}

			// Handle tool use
			if chunk.ToolUse != nil {
				lastToolUse = chunk.ToolUse
			}

			if chunk.Thinking != "" {
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeThinking,
					Content:   chunk.Thinking,
					Timestamp: time.Now(),
				})
			}
		}

		// Add complete response to conversation
		assistantMsg := response.String()
		a.mu.Lock()
		a.conversation = append(a.conversation, chat.NewAssistantMessage(assistantMsg))
		a.mu.Unlock()

		if lastToolUse != nil {
			// Send tool use notification
			a.sendMessage(Message{
				ID:        generateID(),
				AgentID:   a.id,
				Type:      MessageTypeToolUse,
				Content:   fmt.Sprintf("Using tool: %s", lastToolUse.Name),
				Metadata:  map[string]interface{}{"tool": lastToolUse.Name},
				Timestamp: time.Now(),
			})

			// Execute tool
			result, err := a.executeTool(ctx, *lastToolUse)
			if err != nil {
				a.sendMessage(Message{
					ID:        generateID(),
					AgentID:   a.id,
					Type:      MessageTypeError,
					Content:   fmt.Sprintf("Tool %s failed: %v", lastToolUse.Name, err),
					Timestamp: time.Now(),
				})
				return
			}

			// Add tool result to conversation and loop
			a.mu.Lock()
			toolMsg := chat.NewToolResultMessage(lastToolUse.Name, lastToolUse.ID, result.Content, result.Success)
			a.conversation = append(a.conversation, toolMsg)
			a.mu.Unlock()

			continue
		}

		// No more tool uses, we are done
		a.sendMessage(Message{
			ID:        generateID(),
			AgentID:   a.id,
			Type:      MessageTypeResult,
			Content:   assistantMsg,
			Timestamp: time.Now(),
		})
		break
	}
}

// getAvailableTools returns tools filtered by persona
func (a *BaseAgent) getAvailableTools() []llm.Tool {
	allowedTools := a.persona.AvailableTools()
	if len(allowedTools) == 0 {
		return nil
	}

	allTools := a.skillRegistry.ListTools()
	var tools []llm.Tool

	for _, tool := range allTools {
		match := false
		for _, pattern := range allowedTools {
			// Direct match
			if tool.Name() == pattern {
				match = true
				break
			}
			// Glob match
			if matched, _ := filepath.Match(pattern, tool.Name()); matched {
				match = true
				break
			}
		}

		if match {
			tools = append(tools, llm.Tool{
				Name:        tool.Name(),
				Description: tool.Description(),
				InputSchema: tool.Schema(),
			})
		}
	}
	return tools
}

// generateID creates a unique identifier
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// AgentError represents an agent-specific error
type AgentError struct {
	agentID string
	message string
}

func newAgentError(agentID, message string) *AgentError {
	return &AgentError{
		agentID: agentID,
		message: message,
	}
}

func (e *AgentError) Error() string {
	return fmt.Sprintf("agent %s: %s", e.agentID, e.message)
}
