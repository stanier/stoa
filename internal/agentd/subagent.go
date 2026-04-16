package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/skills"
)

// noopSendEvent is a no-op event sink used for child agent loops whose events
// are not forwarded to the parent WebSocket connection.
var noopSendEvent = func(agentproto.Event) {}

// ─── WaitForMessageTool ──────────────────────────────────────────────────────

// WaitForMessageTool is registered only in child sessions. It blocks until the
// parent sends a message via MessageAgentTool or the context is cancelled.
type WaitForMessageTool struct {
	sa *SubAgent
}

func (t *WaitForMessageTool) Name() string { return "wait_for_message" }

func (t *WaitForMessageTool) Description() string {
	return "Block until the parent agent sends a message via message_agent. Returns the message content."
}

func (t *WaitForMessageTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{Type: "object", Properties: map[string]interface{}{}}
}

func (t *WaitForMessageTool) Execute(ctx context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	select {
	case msg, ok := <-t.sa.inbox:
		if !ok {
			return skills.ToolResult{Success: false, Error: "agent was cancelled"}, nil
		}
		return skills.ToolResult{Success: true, Content: msg}, nil
	case <-ctx.Done():
		return skills.ToolResult{Success: false, Error: "timeout waiting for message"}, nil
	}
}

// ─── SpawnAgentTool ──────────────────────────────────────────────────────────

// SpawnAgentTool lets the LLM spawn an async child agent. Returns immediately
// with {"agent_id": "..."} so the parent can continue. The child result is
// delivered later as a synthetic user message.
type SpawnAgentTool struct {
	s         *Server
	sess      *Session
	sendEvent func(agentproto.Event)
}

func (t *SpawnAgentTool) Name() string { return "spawn_agent" }

func (t *SpawnAgentTool) Description() string {
	return "Spawn an async sub-agent to work on a task. Returns immediately with {agent_id}. " +
		"The agent runs concurrently and its result will be delivered as a user message when complete."
}

func (t *SpawnAgentTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task description for the sub-agent to work on",
			},
			"persona": map[string]interface{}{
				"type":        "string",
				"description": "The persona for the sub-agent (e.g. 'coder', 'analyzer', 'default')",
			},
		},
		Required: []string{"task"},
	}
}

func (t *SpawnAgentTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var input struct {
		Task    string `json:"task"`
		Persona string `json:"persona"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return skills.ToolResult{Success: false, Error: "invalid parameters: " + err.Error()}, nil
	}
	if input.Task == "" {
		return skills.ToolResult{Success: false, Error: "task is required"}, nil
	}
	if input.Persona == "" {
		input.Persona = "default"
	}

	agentID := uuid.New().String()
	childCtx, cancel := context.WithTimeout(context.Background(), agenticLoopTimeout)

	inbox := make(chan string, 4)
	sa := &SubAgent{
		id:      agentID,
		task:    input.Task,
		persona: input.Persona,
		status:  SubAgentStatusRunning,
		cancel:  cancel,
		inbox:   inbox,
	}

	t.sess.mu.Lock()
	t.sess.subAgents[agentID] = sa
	t.sess.mu.Unlock()

	// Build the child LLM context (system prompt + tools from global registry).
	childLLMCtx := app.BuildLLMContext(
		t.s.cfg,
		t.s.skillRegistry,
		t.s.promptReg,
		t.s.personaReg,
		input.Persona,
		nil,
	)

	// Build child message history: system prompt + task with sub-agent context.
	var childMessages []chat.Message
	if childLLMCtx.SystemPrompt != "" {
		childMessages = append(childMessages, systemMessage(childLLMCtx.SystemPrompt))
	}
	taskMsg := fmt.Sprintf(
		"You are operating as a sub-agent (ID: %s, persona: %s).\n\nYour task:\n\n%s\n\n"+
			"Note: If you need input from the parent agent, use the wait_for_message tool.",
		agentID, input.Persona, input.Task,
	)
	childMessages = append(childMessages, chat.NewUserMessage(taskMsg))

	childSess := newChildSession(agentID, input.Persona, childMessages, childLLMCtx)

	// Add wait_for_message to the child session — both as an executable tool and
	// as an LLM-visible tool definition so the model knows it can call it.
	waitTool := &WaitForMessageTool{sa: sa}
	childSess.mu.Lock()
	childSess.perSessionTools["wait_for_message"] = waitTool
	childSess.llmCtx.Tools = append(childSess.llmCtx.Tools, llm.Tool{
		Name:        waitTool.Name(),
		Description: waitTool.Description(),
		InputSchema: waitTool.Schema(),
	})
	childSess.mu.Unlock()

	parentResultQueue := t.sess.resultQueue

	go func() {
		defer cancel()

		finalContent, _, _, _, err := runAgenticLoop(childCtx, t.s, childSess, noopSendEvent)

		t.sess.mu.Lock()
		if sa.status == SubAgentStatusRunning {
			if err != nil {
				sa.status = SubAgentStatusError
			} else {
				sa.status = SubAgentStatusCompleted
			}
		}
		t.sess.mu.Unlock()

		result := subAgentResult{agentID: agentID}
		if err != nil {
			result.content = err.Error()
			result.isError = true
		} else {
			result.content = finalContent
		}

		select {
		case parentResultQueue <- result:
		default:
			logging.Warnf("agentd: resultQueue full, dropping result for agent %s", agentID)
		}
	}()

	// Emit observability event immediately so the client knows the agent was spawned.
	t.sendEvent(agentproto.Event{
		Type: agentproto.EventTypeAgentSpawned,
		AgentSpawned: &agentproto.AgentSpawnedEvent{
			AgentID: agentID,
			Task:    input.Task,
			Persona: input.Persona,
		},
	})

	resultJSON, _ := json.Marshal(map[string]string{"agent_id": agentID})
	return skills.ToolResult{Success: true, Content: string(resultJSON)}, nil
}

// ─── MessageAgentTool ────────────────────────────────────────────────────────

// MessageAgentTool lets the parent LLM send a message to a running child agent
// that is blocked in WaitForMessageTool.
type MessageAgentTool struct {
	sess *Session
}

func (t *MessageAgentTool) Name() string { return "message_agent" }

func (t *MessageAgentTool) Description() string {
	return "Send a message to a running sub-agent. The agent must be awaiting input via wait_for_message."
}

func (t *MessageAgentTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"agent_id": map[string]interface{}{
				"type":        "string",
				"description": "The agent ID returned by spawn_agent",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "The message to send to the agent",
			},
		},
		Required: []string{"agent_id", "message"},
	}
}

func (t *MessageAgentTool) Execute(_ context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var input struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return skills.ToolResult{Success: false, Error: "invalid parameters: " + err.Error()}, nil
	}

	// Capture the inbox reference under the lock, then release before channel op.
	t.sess.mu.Lock()
	sa, ok := t.sess.subAgents[input.AgentID]
	var inboxRef chan string
	if ok && sa.status == SubAgentStatusRunning {
		inboxRef = sa.inbox
	}
	t.sess.mu.Unlock()

	if inboxRef == nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("agent %s not found or not running", input.AgentID),
		}, nil
	}

	select {
	case inboxRef <- input.Message:
		return skills.ToolResult{Success: true, Content: "message sent"}, nil
	default:
		return skills.ToolResult{Success: false, Error: "agent inbox is full"}, nil
	}
}

// ─── ListAgentsTool ──────────────────────────────────────────────────────────

// ListAgentsTool returns all sub-agents for the current session with their status.
type ListAgentsTool struct {
	sess *Session
}

func (t *ListAgentsTool) Name() string        { return "list_agents" }
func (t *ListAgentsTool) Description() string { return "List all sub-agents for the current session with their status." }
func (t *ListAgentsTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{Type: "object", Properties: map[string]interface{}{}}
}

func (t *ListAgentsTool) Execute(_ context.Context, _ json.RawMessage) (skills.ToolResult, error) {
	type agentInfo struct {
		AgentID string `json:"agent_id"`
		Task    string `json:"task"`
		Persona string `json:"persona"`
		Status  string `json:"status"`
	}

	t.sess.mu.Lock()
	agents := make([]agentInfo, 0, len(t.sess.subAgents))
	for _, sa := range t.sess.subAgents {
		agents = append(agents, agentInfo{
			AgentID: sa.id,
			Task:    sa.task,
			Persona: sa.persona,
			Status:  string(sa.status),
		})
	}
	t.sess.mu.Unlock()

	data, err := json.Marshal(agents)
	if err != nil {
		return skills.ToolResult{Success: false, Error: err.Error()}, nil
	}
	return skills.ToolResult{Success: true, Content: string(data)}, nil
}

// ─── CancelAgentTool ─────────────────────────────────────────────────────────

// CancelAgentTool cancels a running sub-agent, unblocking any wait_for_message call.
type CancelAgentTool struct {
	sess *Session
}

func (t *CancelAgentTool) Name() string        { return "cancel_agent" }
func (t *CancelAgentTool) Description() string { return "Cancel a running sub-agent." }
func (t *CancelAgentTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"agent_id": map[string]interface{}{
				"type":        "string",
				"description": "The agent ID to cancel",
			},
		},
		Required: []string{"agent_id"},
	}
}

func (t *CancelAgentTool) Execute(_ context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return skills.ToolResult{Success: false, Error: "invalid parameters: " + err.Error()}, nil
	}

	t.sess.mu.Lock()
	sa, ok := t.sess.subAgents[input.AgentID]
	if !ok {
		t.sess.mu.Unlock()
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("agent %s not found", input.AgentID),
		}, nil
	}
	if sa.status != SubAgentStatusRunning {
		t.sess.mu.Unlock()
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("agent %s is not running (status: %s)", input.AgentID, sa.status),
		}, nil
	}
	sa.status = SubAgentStatusCancelled
	sa.closeInbox()
	cancelFn := sa.cancel
	t.sess.mu.Unlock()

	cancelFn()
	return skills.ToolResult{Success: true, Content: fmt.Sprintf("agent %s cancelled", input.AgentID)}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// newChildSession creates a Session for a child agent. Child sessions have a
// non-nil but small resultQueue (child agents cannot themselves spawn agents in
// this implementation).
func newChildSession(id, persona string, messages []chat.Message, llmCtx app.LLMContext) *Session {
	return &Session{
		id:              id,
		persona:         persona,
		messages:        messages,
		llmCtx:          llmCtx,
		permPending:     make(map[string]chan bool),
		subAgents:       make(map[string]*SubAgent), // no nesting
		resultQueue:     make(chan subAgentResult, 1), // non-nil; unused in practice
		perSessionTools: make(map[string]skills.Tool),
		resultCache:     make(map[string]string),
		createdAt:       time.Now(),
	}
}

// cancelAllSubAgents cancels every running sub-agent of sess. Safe to call
// concurrently; uses the mutex to collect agents before cancelling.
func cancelAllSubAgents(sess *Session) {
	sess.mu.Lock()
	var toCancel []*SubAgent
	for _, sa := range sess.subAgents {
		if sa.status == SubAgentStatusRunning {
			sa.status = SubAgentStatusCancelled
			sa.closeInbox()
			toCancel = append(toCancel, sa)
		}
	}
	sess.mu.Unlock()

	for _, sa := range toCancel {
		sa.cancel()
	}
}

// registerSubAgentTools installs real tool implementations into sess.perSessionTools.
// The global registry already contains stubs for system-prompt visibility;
// executeToolCall checks perSessionTools first so real implementations shadow them.
func registerSubAgentTools(s *Server, sess *Session, sendEvent func(agentproto.Event)) {
	tools := []skills.Tool{
		&SpawnAgentTool{s: s, sess: sess, sendEvent: sendEvent},
		&MessageAgentTool{sess: sess},
		&ListAgentsTool{sess: sess},
		&CancelAgentTool{sess: sess},
	}
	sess.mu.Lock()
	for _, t := range tools {
		sess.perSessionTools[t.Name()] = t
	}
	sess.mu.Unlock()
}
