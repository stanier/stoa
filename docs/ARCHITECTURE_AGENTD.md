# agentd Architecture

This document describes the architecture of the agentd daemon and its relationship to clients. All implementation phases are complete.

## Design Goals

1. **Decouple reasoning from rendering** — The TUI becomes a thin rendering client; all LLM/MCP/tool/compaction logic moves to a daemon (`agentd`).
2. **Multiple frontends from one backend** — TUI, Zulip, Slack, or any other client connects to the same agentd over a stable protocol.
3. **Zulip as a separate process** — The Zulip bridge becomes its own binary that translates between Zulip events and the agentd protocol. No Zulip code in the core agent.

---

## Architecture

```
┌──────────────────────┐        WebSocket        ┌─────────────────────────────────┐
│   cmd/chat (TUI)     │ ◄─────────────────────► │         cmd/agentd              │
│   thin renderer      │                          │                                 │
│   - bubbletea UI     │                          │  - LLM client (OpenAI/Anthropic)│
│   - permission modal │                          │  - MCP manager                  │
│   - viewport/input   │        WebSocket         │  - Skill registry               │
└──────────────────────┘                          │  - Session management           │
                                                  │  - Agentic loop                 │
┌──────────────────────┐        WebSocket         │  - Compaction                   │
│ cmd/zulip-bridge     │ ◄─────────────────────► │  - Permission checker           │
│                      │                          │  - Tracer                       │
│  - Zulip polling     │                          └─────────────────────────────────┘
│  - Reaction handler  │
│  - Thread mapping    │
└──────────────────────┘

Future bridges: cmd/slack-bridge, cmd/discord-bridge, cmd/webhook-bridge, ...
```

---

## Protocol: `internal/agentproto`

Shared types used by all clients and the server.

### HTTP Endpoints (session management)

```
POST /sessions                  Create new session → { "session_id": "..." }
GET  /sessions                  List all sessions
GET  /sessions/:id              Get session info
DELETE /sessions/:id            Close session
```

### WebSocket Endpoint (streaming)

```
WS /sessions/:id/stream
```

Bidirectional JSON framing over a single WebSocket connection per session.

#### Client → Server messages

```json
{ "type": "send_message", "content": "Hello" }
{ "type": "perm_response", "request_id": "req-123", "approved": true }
{ "type": "cancel" }
```

#### Server → Client events

```json
{ "type": "chunk",        "chunk":       { "content": "...", "thinking": "..." } }
{ "type": "done",         "done":        { "full_content": "...", "full_thinking": "..." } }
{ "type": "tool_invoked", "tool_invoked":{ "tool_name": "...", "tool_use_id": "...", "params": {} } }
{ "type": "tool_result",  "tool_result": { "tool_name": "...", "tool_use_id": "...", "result": "...", "is_error": false } }
{ "type": "perm_request", "perm_request":{ "request_id": "...", "tool_name": "...", "description": "...", "params": {}, "rank": 2 } }
{ "type": "perm_mention", "perm_mention":{ "tool_name": "...", "description": "..." } }
{ "type": "compaction",   "compaction":  { "messages_before": 40, "messages_after": 5, "summary": "..." } }
{ "type": "error",        "error":       { "message": "...", "fatal": false } }
```

---

## Component: agentd (`cmd/agentd/`)

The daemon owns all reasoning state.

### Session lifecycle

Each session is an independent conversation:
- Created with an optional persona name
- Has its own message history, MCP connections (shared manager), skill registry
- Permission requests block within the session goroutine; response comes via WS message
- Multiple clients can attach to the same session (future: collaboration)

### Key types

```go
type Session struct {
    id          string
    persona     string
    messages    []chat.Message
    llmCtx      app.LLMContext      // system prompt + tools (coherent pair)
    permPending map[string]chan bool // requestID → response channel
    mu          sync.Mutex
}
```

### agentic loop (moved from `cmd/server/main.go:runAgenticLoop`)

```
SendMessage received
  ↓
Validate message sequence
  ↓
Call llmClient.StreamMessage(ctx, messages, tools)
  ↓
For each StreamChunk:
  - content/thinking → emit ChunkEvent to WS
  - ToolUse → emit ToolInvokedEvent → execute → emit ToolResultEvent
               Permission check:
                 RankSilent/Mention → continue, emit PermMentionEvent
                 RankRequest → emit PermRequestEvent, block on chan
                               client sends perm_response → unblock
                               if denied → inject error as user message
  ↓
StreamDone → emit DoneEvent
  ↓
Check compaction → if needed, run → emit CompactionEvent
```

---

## Component: Thin TUI (`cmd/chat/`)

### What stays in the TUI

- Bubbletea `Model` with viewport, textarea, key bindings
- Permission prompt modal (same UI, triggers `conn.RespondToPermission`)
- Streaming render (chunk accumulation, thinking box)
- Local `/help`, `/clear` commands (these don't need the server)

### What is removed from the TUI

- `llmClient` field and all LLM calls
- `skillRegistry` and MCP setup
- `compactionManager`
- `agentManager`, `personaRegistry`
- `streamFromLLM()` — replaced by `conn.SendMessage()`
- `permChecker`, `permManager`, `permRequestChan`

### New Model core

```go
type Model struct {
    conn      agentproto.Client
    sessionID string
    events    <-chan agentproto.Event  // live stream

    messages         []chat.Message   // local render copy
    currentThinking  string
    currentContent   string
    isStreaming       bool

    viewport  viewport.Model
    textarea  textarea.Model
    activePermissionPrompt *PermissionPromptModel
}
```

### Event loop replaces waitForStreamChunk

```go
func waitForEvent(events <-chan agentproto.Event) tea.Cmd {
    return func() tea.Msg {
        e, ok := <-events
        if !ok {
            return agentDisconnectedMsg{}
        }
        return agentEventMsg{e}
    }
}
```

---

## Component: Zulip Bridge (`cmd/zulip-bridge/`)

A standalone process; no LLM knowledge. Pure translator.

### Responsibilities

- Poll Zulip for new messages via existing `internal/zulip` client
- Forward messages to agentd as `send_message`
- Forward agentd `chunk`/`done` events back to Zulip as messages
- On `perm_request`: post to Zulip with ✅/❌ reactions, wait for reaction, send `perm_response`
- On `perm_mention`: post FYI message to Zulip
- On `compaction`: optionally post a note

### Session mapping

```go
type Bridge struct {
    agentURL  string
    sessionID string           // one session per bridge instance (or per Zulip stream)
    client    *agentproto.WSClient

    // Zulip-specific
    zulipClient  *zulip.Client
    pendingPerms map[string]*pendingPerm  // requestID → zulip message + chan
}

type pendingPerm struct {
    zulipMsgID  int
    requestID   string
}
```

The reaction-based approval flow stays entirely in this process — it's Zulip UX, not agent logic.

---

## File Changes Summary

| Path | Action | Notes |
|------|--------|-------|
| `docs/AGENTD_REFACTOR.md` | New | This document |
| `internal/agentproto/` | New | Shared protocol types + client interface |
| `cmd/agentd/` | New | Agent daemon with WebSocket server |
| `cmd/zulip-bridge/` | New | Extracted from `cmd/server/` |
| `cmd/chat/` | Modify | `NewWithConfig` takes `agentproto.Client` |
| `internal/ui/model.go` | Modify | Remove LLM/MCP/registry fields, add `conn` |
| `internal/ui/handlers.go` | Modify | Handle `agentEventMsg` instead of stream msgs |
| `internal/ui/update.go` | Modify | Remove `streamFromLLM`, `sendToLLM` |
| `internal/app/setup.go` | Deleted | Split into `llm.go`, `mcp.go`, `permissions.go`, `skills.go`, `trace.go`, `personas.go` in `internal/agentd/` |
| `internal/llm/` | Unchanged | Used only by agentd now |
| `internal/mcp/` | Unchanged | Used only by agentd now |
| `internal/skills/` | Unchanged | Used only by agentd now |
| `internal/channels/` | Bridge-only | TUI no longer imports |
| `internal/zulip/` | Bridge-only | TUI no longer imports |
| `cmd/server/` | Removed | Deleted; superseded by agentd + zulip-bridge |
| `Makefile` | Modify | Add `build-agentd`, `build-zulip-bridge` targets |

---

## Implementation Phases (all complete)

### Phase 1 ✅ — `internal/agentproto/` foundation
### Phase 2 ✅ — agentd daemon, TUI refactor, zulip-bridge extraction
### Phase 3 ✅ — integration, Makefile, full compile

---

## Migration Notes (completed)

- `cmd/server/` removed
- `internal/app/setup.go` deleted — logic split into focused files under `internal/agentd/`
- Config format unchanged — agentd reads `~/.config/stoa/config.yaml`
- `agent.address` config key added (env: `AGENT_ADDRESS`)
