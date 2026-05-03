package agentd

import (
	"context"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/skills"
	"github.com/stanier/stoa/internal/trace"
)

// SubAgentStatus represents the current state of a sub-agent.
type SubAgentStatus string

const (
	SubAgentStatusRunning   SubAgentStatus = "running"
	SubAgentStatusCompleted SubAgentStatus = "completed"
	SubAgentStatusCancelled SubAgentStatus = "cancelled"
	SubAgentStatusError     SubAgentStatus = "error"
)

// SubAgent tracks a spawned child agent within a parent session.
type SubAgent struct {
	id        string
	task      string
	persona   string
	status    SubAgentStatus
	cancel    context.CancelFunc
	inbox     chan string     // buffered 4; message_agent writes, wait_for_message reads
	closeOnce sync.Once      // ensures inbox is closed exactly once
	messages  []chat.Message // child's own history; never shared with parent
}

// closeInbox closes the inbox channel exactly once, preventing double-close panics.
func (sa *SubAgent) closeInbox() {
	sa.closeOnce.Do(func() { close(sa.inbox) })
}

// subAgentResult carries the final result of a completed sub-agent back to the
// parent session's resultQueue.
type subAgentResult struct {
	agentID string
	content string
	isError bool
}

// Session represents a single independent conversation.
// Each session has its own message history, LLM context, and permission channels
// so that concurrent sessions do not interfere with each other.
type Session struct {
	id      string
	persona string
	source  string // client that created the session ("web", "tui", "zulip", etc.)
	userID  string // authd user UUID; empty for service/anonymous sessions
	llmKey  string // per-user stoa-infgw_* key provisioned by authd at login; injected into LLM calls
	title   string // auto-generated conversation title; empty until first generation

	// persistedMsgCount is the high-water mark for DB message writes.
	persistedMsgCount int

	// overrides holds per-session LLM parameter overrides set via the web UI
	// or update_config WebSocket message. Nil fields fall back to global config.
	overrides *llm.Overrides

	// Conversation history for this session.
	messages []chat.Message

	// llmCtx holds the system prompt and filtered tool list for this session.
	// Rebuilt at session creation and again after MCP tools register.
	llmCtx app.LLMContext

	// toolsRevealed tracks whether the LLM has called list_tools yet.
	// Until it does, only list_tools (and any pinned tools) are passed to the API.
	// After the first call, the full llmCtx.Tools set is used.
	toolsRevealed bool

	// permPending maps permission request ID → response channel.
	// The agentic loop blocks on the channel; the WebSocket read loop sends to it.
	permPending map[string]chan bool

	// subAgents tracks all child agents spawned by this session.
	subAgents map[string]*SubAgent

	// resultQueue receives completed sub-agent results for injection into the
	// parent conversation. Buffered to handle burst completions without blocking.
	resultQueue chan subAgentResult

	// perSessionTools holds tool implementations that are specific to this session
	// (e.g. spawn_agent, message_agent). Checked before the global skill registry
	// in executeToolCall so real implementations shadow global stubs.
	perSessionTools map[string]skills.Tool

	// resultCache stores full tool result content for large results that were
	// truncated before being sent to the LLM. Keyed by a short ID included in
	// the truncation notice so the LLM can retrieve subsequent pages via the
	// read_tool_result per-session tool.
	resultCache map[string]string

	// allowedTools restricts which tools are available for LLM calls in this session.
	// Nil means the persona's full tool set; non-nil is an additional glob filter.
	allowedTools []string

	// tracer logs conversation events to a per-session JSONL file.
	tracer *trace.Tracer

	// Real token counts from LLM API responses. Zero until the first turn completes.
	// lastInputTokens is overwritten each turn (LLM input already contains full history).
	// totalOutputTokens accumulates across turns (each turn only generates new tokens).
	lastInputTokens   int
	totalOutputTokens int

	createdAt time.Time

	mu sync.Mutex
}

// newSession creates an initialised Session.
func newSession(id, persona, source, userID, llmKey string, allowedTools []string) *Session {
	return &Session{
		id:              id,
		persona:         persona,
		source:          source,
		userID:          userID,
		llmKey:          llmKey,
		allowedTools:    allowedTools,
		messages:        []chat.Message{},
		permPending:     make(map[string]chan bool),
		subAgents:       make(map[string]*SubAgent),
		resultQueue:     make(chan subAgentResult, 20),
		perSessionTools: make(map[string]skills.Tool),
		resultCache:     make(map[string]string),
		createdAt:       time.Now(),
	}
}
