# Runtime Architecture

How the components fit together at runtime. For deployment specifics see [CONTAINERIZATION.md](CONTAINERIZATION.md); for remaining work see [remaining/README.md](remaining/README.md).

---

## Component Map

```
Browser / TUI client
       │
       │  HTTP / WebSocket
       ▼
  ┌─────────┐     HTTP      ┌─────────┐     HTTP      ┌────────────┐
  │  webd   │ ────────────► │  authd  │ ────────────► │  local-idp │
  │ :3000   │               │  :8888  │               │  :9999     │
  └────┬────┘               └────┬────┘               └────────────┘
       │                         │ Postgres                  │ Postgres
       │ HTTP / WebSocket         ▼                           ▼
       │                   postgres-authd             postgres-localidp
       ▼
  ┌─────────┐     Docker exec    ┌──────────────────────────────────┐
  │  agentd │ ─────────────────► │  MCP containers                  │
  │  :7777  │                    │  mcp-shell, mcp-netbox, mcp-*    │
  └────┬────┘                    └──────────────────────────────────┘
       │ Postgres (optional)          │ HTTP_PROXY (isolated nets)
       ▼                             ▼
  postgres-agentd               squid proxy

  ┌─────────────────┐     WebSocket     ┌──────────────────┐
  │  zulip-bridge   │ ───────────────► agentd
  └─────────────────┘
```

**Network isolation**: Each Postgres is on a private bridge reachable only by its own service. MCP containers on `mcp-*-net` (all `internal: true`) cannot make direct outbound connections — only via squid. `agent-external` is the only bridge with internet egress, and only agentd, webd, authd, zulip-bridge, and inference-gateway sit on it.

---

## Binaries

| Binary | Role | Key env vars |
|--------|------|-------------|
| `chat` | Bubbletea TUI; thin client in remote mode, embedded LLM in standalone | `AGENT_ADDRESS`, `LLM_PROVIDER`, `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` |
| `agentd` | Agentic loop daemon; owns LLM, MCP, sessions, permissions | `LLM_PROVIDER`, `*_API_KEY`, `AGENTD_API_KEY`, `AGENTD_AUTHD_URL`, `AGENTD_DATABASE_URL`, `DOCKER_HOST` |
| `authd` | OAuth2 identity broker; issues and validates session tokens | `AUTHD_DATABASE_URL`, `AUTHD_SIGNING_KEY`, `AUTHD_EXTERNAL_URL`, `AUTHD_ALLOWED_REDIRECT_ORIGINS` |
| `local-idp` | Local OIDC provider; argon2id passwords, account lockout | `LOCAL_IDP_DATABASE_URL`, `LOCAL_IDP_ISSUER`, `LOCAL_IDP_EXTERNAL_URL` |
| `webd` | Web BFF; serves Svelte SPA, sets HttpOnly cookie, proxies to agentd | `WEBD_AUTHD_URL`, `WEBD_AGENTD_URL`, `WEBD_EXTERNAL_URL`, `WEBD_S3_*` |
| `garage` | S3-compatible object store (Garage); holds file uploads from webd | `GARAGE_ACCESS_KEY_ID`, `GARAGE_SECRET_ACCESS_KEY`, `GARAGE_BUCKET` |
| `zulip-bridge` | Translates Zulip events ↔ agentd WebSocket protocol | `AGENT_ADDRESS`, `ZULIP_SITE_URL`, `ZULIP_API_KEY`, `AGENTD_API_KEY` |
| `auth-admin` | CLI for DB migrations, user management, service tokens | `AUTHD_MIGRATE_DATABASE_URL`, `LOCAL_IDP_MIGRATE_DATABASE_URL` |
| `mcp-*` | Standalone MCP servers (one binary each); communicate via stdio | Per-server credentials in `config.yaml` |

**Startup order** (containerized): Postgres instances → migrate containers → local-idp, authd → agentd, garage → garage-init, webd → zulip-bridge, chat.

**File upload flow (S3 mode)**: Browser `POST /api/upload` → webd stores object in Garage via `PutObject` → generates a presigned `GetObject` URL (default TTL 7 days) → returns URL to browser. The LLM receives the URL in the message and fetches the file via `mcp_fetch`. Credentials never leave the server. In local/dev mode (no `WEBD_S3_ENDPOINT`), webd falls back to writing to `WEBD_UPLOAD_DIR` on the local filesystem and the LLM uses `mcp_filesystem_read_file` instead.

---

## Request Lifecycle: TUI keystroke → LLM response

### 1. TUI sends message (`internal/ui/handlers_input.go:84`)

User presses Enter. TUI appends a `UserMessage` to its local message slice, then — if `m.conn != nil` (remote mode) — calls:

```go
m.conn.SendMessage(content, clientMsg)
return m, waitForEvent(m.conn.Events())
```

This serialises a `ClientMessage{Type: "send_message", SendMessage: {Content: ...}}` as JSON over the `golang.org/x/net/websocket` connection. It also starts a `waitForEvent` Bubbletea command that blocks on the event channel until the next frame arrives.

### 2. agentd receives and dispatches (`internal/agentd/handlers.go:533`)

`serveWebSocket` runs two concurrent goroutines per connection: a **read loop** that decodes incoming `ClientMessage` frames, and a **permForwarder** that forwards permission request events to the client. When a `send_message` arrives the read loop acquires `agentSem` (prevents concurrent loops on the same session) and calls `handleMessage()` in a new goroutine.

### 3. Agentic loop (`internal/agentd/loop.go:43`)

```
runAgenticLoop:
  lock sess, append UserMessage
  unlock

  iterate (tool calls loop):
    snapshot messages + tools
    llmClient.StreamMessage(ctx, msgs, tools) → chan StreamChunk

    for chunk := range responseChan:
      chunk.Content  → sendEvent(EventTypeChunk{content})
      chunk.Thinking → sendEvent(EventTypeChunk{thinking})
      chunk.Done     → accumulate InputTokens / OutputTokens
      chunk.ToolUse  → executeToolCall() [see below]

    if no tool call: sendEvent(EventTypeDone), break
    if tool call: append tool result as UserMessage, loop
```

Each `sendEvent` call serialises an `agentproto.Event` as JSON and writes it to the WebSocket. Events are emitted in real time as chunks arrive from the LLM API.

### 4. TUI renders events (`internal/ui/handlers_agent.go:57`)

| Event | TUI action |
|-------|-----------|
| `EventTypeChunk` | Append to `m.currentStreamMsg` / `m.currentThinking`; call `updateViewport()` (fast path: only re-renders the streaming message, not full history — `stableRenderedCache` holds the rest) |
| `EventTypeToolInvoked` | Inline tool-call display in streaming message |
| `EventTypeToolResult` | Append tool result message; add empty assistant placeholder |
| `EventTypePermRequest` | Show permission modal; user approves/denies; send `perm_response` ClientMessage |
| `EventTypeDone` | Finalise content from `e.Done.FullContent`; store `e.Done.InputTokens` / `OutputTokens` in model; clear streaming state |
| `EventTypeCompaction` | Show compaction notice in conversation |
| `EventTypeError` | Show error; if `Fatal: true` close session |

After each event the handler returns a new `waitForEvent` command, keeping the loop alive until `EventTypeDone` or disconnect.

---

## Tool Call Flow

### Detection

`StreamChunk.ToolUse` is non-nil when the LLM requests a tool. The loop pauses streaming, runs the tool, appends the result as a `UserMessage`, then calls `StreamMessage` again with the updated history (next iteration).

### Permission check (`internal/agentd/loop.go:148`)

Before executing, the loop calls `permsSetup.Checker.Check(ctx, toolUse.Name, "", params)`. Ranks:

| Rank | Behaviour |
|------|-----------|
| 0 (Silent) | Execute immediately, no notification |
| 1 (Mention) | Execute immediately, emit `EventTypePermMention` |
| 2 (Request) | Emit `EventTypePermRequest`; **block** on `sess.permPending[requestID]` channel until TUI sends `perm_response`; abort if denied |

### Lookup and execution (`internal/agentd/loop.go:256`)

```
1. Check sess.perSessionTools[name]  (sub-agent tools, read_tool_result, etc.)
2. Check s.skillRegistry.GetTool(name)  (global: skills + all MCP tools)
3. tool.Execute(ctx, params) → ToolResult
4. If result > 50KB: store in sess.resultCache, return truncated + page notice
5. Append result as UserMessage; shouldContinue = true → next loop iteration
```

MCP tools are registered as adapters (`internal/mcp/adapter.go`) that call `client.CallTool()` over the MCP transport. Skills (slash commands registered as tools) call their `Execute` method directly in-process.

---

## Auth Flow

### Browser login (webd → authd → local-idp)

```
1. Browser: GET /auth/login
   webd calls authd POST /oauth/login
   authd returns { login_id, url }

2. authd redirects browser to local-idp:
   GET local-idp:9999/authorize?client_id=...&redirect_uri=http://authd:8888/oauth/callback&state=...

3. local-idp shows login form; user submits username/password
   local-idp verifies argon2id hash, checks account lockout
   local-idp redirects: GET authd:8888/oauth/callback?code=...&state=...

4. authd exchanges code for ID token (calls local-idp /token)
   authd verifies ID token signature (JWK from local-idp /.well-known/jwks.json)
   authd issues HMAC-signed session token; stores in Postgres (authdb)
   authd redirects: GET webd:3000/?token=<session-token>

5. webd calls authd POST /token/verify to validate
   webd sets HttpOnly SameSite cookie containing session-token
   webd redirects browser to /
```

**Key gotcha**: `LOCAL_IDP_ISSUER` (internal name, used by authd for OIDC discovery) must differ from `LOCAL_IDP_EXTERNAL_URL` (browser-reachable URL, used only for `authorization_endpoint`). Setting them the same breaks either authd discovery or the login redirect.

### agentd token validation (`internal/agentd/auth.go`)

Every request to agentd (except `/healthz`) passes through `AuthMiddlewareWithAuthd`. Validation priority:

```
1. authdClient.VerifyToken(bearer)     if AGENTD_AUTHD_URL is set
2. authDB.ValidateSession(bearer)      local Postgres fallback
3. constant-time compare to AGENTD_API_KEY  local dev / service accounts
4. reject 401
```

On success, `user_id` and `is_service` are attached to the request context. webd injects the session cookie as a `Bearer` header when proxying to agentd.

---

## Session Lifecycle

### Creation (`POST /sessions`, `internal/agentd/handlers.go:29`)

- Allocates `Session` struct with UUID, builds `LLMContext` (system prompt + tool list) via `app.BuildLLMContext()`
- Prepends a system `chat.Message` if system prompt is non-empty
- Registers per-session tools: `list_tools`, `read_tool_result`, sub-agent tools
- Optionally persists to Postgres (`agentDB.CreateSession`) or filesystem (`AGENTD_SESSION_DIR`)

### Session struct (key fields, `internal/agentd/session.go:53`)

```go
type Session struct {
    messages        []chat.Message           // full conversation history
    llmCtx          app.LLMContext           // system prompt + tools (coherent pair)
    permPending     map[string]chan bool      // requestID → approval channel
    perSessionTools map[string]skills.Tool   // session-scoped tools
    resultCache     map[string]string        // paginated large tool results
    lastInputTokens  int                     // from last LLM call
    totalOutputTokens int                    // accumulated across turns
    mu              sync.Mutex
}
```

### Compaction (`internal/agentd/handlers.go:317`)

Auto-triggered after each turn when estimated token count exceeds `compaction.compact_threshold` (default ~40k). The compactor summarises older messages and replaces them with a `SummaryMessage`, then emits `EventTypeCompaction` so the TUI can show a notice. Manual trigger via `POST /sessions/{id}/compact`.

### Persistence

| Mode | Mechanism |
|------|-----------|
| In-memory only | Default; sessions lost on restart |
| Filesystem | `AGENTD_SESSION_DIR`: one JSON file per session, loaded on startup |
| Postgres | `AGENTD_DATABASE_URL`: `sessions` + `messages` tables; written after each turn |

---

## MCP Server Lifecycle

### Startup and discovery (`internal/app/mcp.go:31`)

`SetupMCPManager` returns immediately and runs connection + discovery in a background goroutine with a 15-second timeout. Sessions created during this window have a reduced tool list; once discovery completes, `OnComplete` rebuilds all session `LLMContext` instances with the full tool set.

### Transports (`internal/mcp/transport/`)

| Transport | How it works |
|-----------|-------------|
| `stdio` | Spawns binary as subprocess; JSON-RPC over stdin/stdout |
| `http` | Direct HTTP to an external MCP endpoint |
| `docker` | Pulls image, runs container via Docker/Podman socket (`DOCKER_HOST`); attaches stdio |

For Docker transport, if `network_mode` is an isolated network (e.g., `mcp-shell-net`), the container cannot reach the internet directly. `HTTP_PROXY` / `HTTPS_PROXY` are set to the squid address so outbound HTTP/HTTPS still works but is filtered by squid's domain allowlist.

### Tool registration

Each discovered tool becomes an `MCPToolAdapter` (`internal/mcp/adapter.go`) registered in the global `skillRegistry`. The adapter's `Execute` method calls `client.CallTool()` over the transport. If the result exceeds 50KB it is paged via `sess.resultCache` (see Tool Call Flow above).

### Failure handling

- **Connect failure**: logged as warning; server omitted from tool list
- **Heartbeat failure** (30s interval, 5s timeout): marks connection dead, triggers async reconnect with exponential backoff (1s → 5m)
- **Tool call failure**: returns error string as tool result; conversation continues; LLM sees the error and can retry or report it

---

## ACP Surface (`/acp/`)

Exposes published agents as an ACP-compatible REST+SSE API.

**Discovery:**
- `GET /acp/ping` — health check (no auth required)
- `GET /acp/agents` — list published agents (external agents: no auth; internal: requires Bearer)
- `GET /acp/agents/{name}` — agent manifest

**Runs:**
- `POST /acp/runs` — create run; body: `{"agent_name", "messages", "mode", "session_id?"}`;
  mode: `sync` (default) / `async` / `stream`
- `GET /acp/runs/{id}` — poll run status
- `POST /acp/runs/{id}` — resume awaiting run (`{"request_id", "approved"}`)
- `POST /acp/runs/{id}/cancel` — cancel a running or awaiting run
- `GET /acp/session/{id}` — inspect the underlying session for a run

**Streaming** uses SSE (`text/event-stream`). Events: `message.part`, `message.completed`, `run.completed`, `run.failed`. SSE keepalive comments (`: keepalive`) are sent every 15 s to prevent proxies from dropping idle connections.

**Run → session mapping:** each run targets a session. If `session_id` is omitted a new ephemeral session is created and reused for subsequent runs in the same logical conversation by passing the returned `session_id` back.

**Auth:** same Bearer token as the native API. External-visibility agents allow anonymous callers. `apikeyd`-issued keys (`stoa-infgw_*`) are accepted when `AGENTD_APIKEYD_URL` is configured.

**In-memory run store:** completed/failed/cancelled runs are evicted after 24 h by a background goroutine started in `NewServer`.

---

## Key Files Quick Reference

| Concern | File |
|---------|------|
| Config loading + env var binding | `internal/config/config.go:Load()` |
| TUI remote-mode message send | `internal/ui/handlers_agent.go:sendMessageToAgentD()` |
| TUI event render | `internal/ui/handlers_agent.go:handleAgentEvent()` |
| agentd WebSocket handler | `internal/agentd/handlers.go:serveWebSocket()` |
| Agentic loop + tool execution | `internal/agentd/loop.go:runAgenticLoop()`, `executeToolCall()` |
| Session struct | `internal/agentd/session.go:Session` |
| Auth middleware | `internal/agentd/auth.go:AuthMiddlewareWithAuthd()` |
| MCP background setup | `internal/app/mcp.go:SetupMCPManager()` |
| MCP Docker transport | `internal/mcp/transport/docker.go:NewDockerTransport()` |
| Tool result pagination | `internal/agentd/toolpager.go` |
| Compaction trigger | `internal/agentd/handlers.go:maybeAutoCompact()` |
| Auth broker routes | `internal/authd/server.go` |
| webd cookie + proxy | `internal/webd/server.go` |
