// Package agentproto defines the shared protocol types between agentd and its clients
// (TUI, Zulip bridge, etc.).
package agentproto

import "encoding/json"

// EventType identifies which field in an Event is populated.
type EventType string

const (
	EventTypeChunk          EventType = "chunk"
	EventTypeDone           EventType = "done"
	EventTypeToolInvoked    EventType = "tool_invoked"
	EventTypeToolResult     EventType = "tool_result"
	EventTypePermRequest    EventType = "perm_request"
	EventTypePermMention    EventType = "perm_mention"
	EventTypeCompaction     EventType = "compaction"
	EventTypeError          EventType = "error"
	EventTypeAgentSpawned   EventType = "agent_spawned"
	EventTypeAgentCompleted EventType = "agent_completed"
	EventTypeTitle          EventType = "title"
)

// Event is the server→client message. Exactly one of the payload fields is non-nil.
type Event struct {
	Type           EventType            `json:"type"`
	SessionID      string               `json:"session_id"`
	Chunk          *ChunkEvent          `json:"chunk,omitempty"`
	Done           *DoneEvent           `json:"done,omitempty"`
	ToolInvoked    *ToolInvokedEvent    `json:"tool_invoked,omitempty"`
	ToolResult     *ToolResultEvent     `json:"tool_result,omitempty"`
	PermRequest    *PermRequestEvent    `json:"perm_request,omitempty"`
	PermMention    *PermMentionEvent    `json:"perm_mention,omitempty"`
	Compaction     *CompactionEvent     `json:"compaction,omitempty"`
	Error          *ErrorEvent          `json:"error,omitempty"`
	AgentSpawned   *AgentSpawnedEvent   `json:"agent_spawned,omitempty"`
	AgentCompleted *AgentCompletedEvent `json:"agent_completed,omitempty"`
	Title          *TitleEvent          `json:"title,omitempty"`
}

// ChunkEvent carries an incremental piece of the assistant's response during streaming.
type ChunkEvent struct {
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

// DoneEvent signals the end of a streaming response.
type DoneEvent struct {
	FullContent  string `json:"full_content"`
	FullThinking string `json:"full_thinking,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
}

// ToolInvokedEvent notifies the client that the LLM requested a tool call.
// This is informational — tool execution happens inside agentd.
// RequiresPermission is true when the tool requires explicit user approval before
// running; in that case a PermRequest event will follow and clients may suppress
// this notice to avoid duplicate messaging.
type ToolInvokedEvent struct {
	ToolName          string          `json:"tool_name"`
	ToolUseID         string          `json:"tool_use_id"`
	Params            json.RawMessage `json:"params"`
	RequiresPermission bool           `json:"requires_permission,omitempty"`
}

// ToolResultEvent notifies the client of a completed tool execution.
type ToolResultEvent struct {
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error,omitempty"`
}

// PermRequestEvent asks the client to approve or deny a tool invocation.
// The client must send a ClientMessage with type "perm_response" to unblock execution.
// Rank: 0=Silent, 1=Mention, 2=Request (only Request triggers this event).
type PermRequestEvent struct {
	RequestID   string          `json:"request_id"`
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	Params      json.RawMessage `json:"params"`
	Rank        int             `json:"rank"`
}

// PermMentionEvent informs the client that a tool ran without requiring approval.
type PermMentionEvent struct {
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
}

// CompactionEvent notifies the client that conversation history was compacted.
type CompactionEvent struct {
	MessagesBefore int    `json:"messages_before"`
	MessagesAfter  int    `json:"messages_after"`
	Summary        string `json:"summary"`
}

// ErrorEvent carries an error from the server. If Fatal is true, the session is over.
type ErrorEvent struct {
	Message string `json:"message"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// AgentSpawnedEvent notifies the client that a sub-agent was successfully spawned.
type AgentSpawnedEvent struct {
	AgentID string `json:"agent_id"`
	Task    string `json:"task"`
	Persona string `json:"persona"`
}

// AgentCompletedEvent notifies the client that a sub-agent finished (or errored).
// The Result is also injected into the parent conversation as a user message so
// the LLM can synthesize a final response.
type AgentCompletedEvent struct {
	AgentID string `json:"agent_id"`
	IsError bool   `json:"is_error,omitempty"`
	Result  string `json:"result"`
}

// TitleEvent notifies the client that a session title has been generated or updated.
type TitleEvent struct {
	Title string `json:"title"`
}
