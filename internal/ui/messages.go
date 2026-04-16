package ui

import (
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/skills"
)

// Custom message types for bubbletea's Update function

// errMsg is sent when an error occurs
type errMsg struct {
	err error
}

func (e errMsg) Error() string {
	return e.err.Error()
}

// streamChunkMsg is sent when a chunk of streaming response arrives
type streamChunkMsg struct {
	content  string
	thinking string
	toolUse  *llm.ToolUse
	stream   <-chan llm.StreamChunk
}

// streamDoneMsg is sent when streaming is complete
type streamDoneMsg struct{}

// streamStartMsg is sent when streaming begins
type streamStartMsg struct {
	stream <-chan llm.StreamChunk
}

// compactionNeededMsg is sent when compaction should be performed
type compactionNeededMsg struct {
	currentTokens int
	threshold     int
}

// compactionDoneMsg is sent when compaction is complete
type compactionDoneMsg struct {
	newMessages []chat.Message
	summary     chat.Message
}

// compactionErrorMsg is sent when compaction fails
type compactionErrorMsg struct {
	err error
}

// commandExecutedMsg is sent when a command completes
type commandExecutedMsg struct {
	result skills.CommandResult
}

// toolInvokedMsg is sent when the LLM requests a tool
type toolInvokedMsg struct {
	toolUse llm.ToolUse
}

// toolResultMsg is sent when a tool execution completes
type toolResultMsg struct {
	toolUseID string
	toolName  string
	result    skills.ToolResult
	stream    <-chan llm.StreamChunk
}

// Agent lifecycle messages

// agentSpawnedMsg is sent when a new agent is created
type agentSpawnedMsg struct {
	agent *AgentView
}

// agentMessageMsg is sent when an agent sends a message
type agentMessageMsg struct {
	agentID string
	message agents.Message
}

// agentCompletedMsg is sent when an agent completes its task
type agentCompletedMsg struct {
	agentID string
	content string
}

// agentErrorMsg is sent when an agent encounters an error
type agentErrorMsg struct {
	agentID string
	err     error
}

// agentStoppedMsg is sent when an agent is stopped/killed
type agentStoppedMsg struct {
	agentID string
}

// agentPanelRefreshMsg is sent periodically to refresh the agent panel display
type agentPanelRefreshMsg struct{}

// agentClickedMsg is sent when an agent card is clicked
type agentClickedMsg struct {
	agentID string
}

// tokenCountUpdateMsg is sent when async token count update completes
type tokenCountUpdateMsg struct {
	contextID string // "" for main chat, agentID for agent conversation
	count     int
}

// Permission-related messages

// permissionRequestMsg is sent when a tool requires user permission
type permissionRequestMsg struct {
	request *permissions.PermissionRequest
}

// permissionMentionMsg is sent when a tool execution should be mentioned to the user (Rank 1)
type permissionMentionMsg struct {
	mention *permissions.PermissionMention
}

// mcpToolsRegisteredMsg is sent after MCP servers connect and register their tools.
// Used to trigger a system prompt rebuild so the tool listing reflects MCP tools.
type mcpToolsRegisteredMsg struct {
	count int
}

// Note: Zulip message types are defined in the zulip package and imported as:
// - zulip.MessageReceivedMsg
// - zulip.ConnectionStatusMsg
// - zulip.ReactionReceivedMsg

// Remote agent (agentd) message types

// agentEventMsg carries a server→client event from agentd.
type agentEventMsg struct {
	event agentproto.Event
}

// agentConnectedMsg is sent when the WebSocket connection to agentd is established.
type agentConnectedMsg struct {
	conn      agentproto.ClientConn
	client    agentproto.Client
	sessionID string
}

// agentDisconnectedMsg is sent when the agentd connection is lost or fails to connect.
type agentDisconnectedMsg struct {
	err error
}

// agentConnectingMsg is sent when the TUI starts connecting to agentd.
type agentConnectingMsg struct{}
