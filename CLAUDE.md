# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run

```bash
# Build using Makefile (binaries output to bin/debug/ directory)
make build              # Build all binaries
make build-chat         # Build TUI client
make build-agentd       # Build agent daemon
make build-zulip-bridge # Build Zulip bridge
make build-server       # Build legacy multi-channel server
make build-mcp-all      # Build all MCP server binaries

# Standalone mode (TUI with embedded LLM — original mode)
OPENAI_API_KEY=sk-... ./bin/debug/chat
LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat

# Remote mode (TUI connects to agentd)
# 1. Start agentd daemon
OPENAI_API_KEY=sk-... ./bin/debug/agentd --port 7777
# 2. Connect TUI (no LLM key needed)
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat

# Zulip bridge (connects to running agentd)
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/zulip-bridge

# Other make targets
make clean      # Remove build artifacts
make dev        # Build with race detector
make fmt        # Format code
make test       # Run tests
```

## Architecture Overview

This is an AI agent platform: `agentd` is the core daemon (LLM, MCP, tools, sessions over WebSocket); `webd` serves a Svelte SPA; bridge processes connect external chat platforms (Zulip, Slack, Discord, Matrix); and `chat` is a Bubbletea TUI client. All clients follow the Elm Architecture (Model-Update-View) and share the `agentproto` WebSocket protocol.

### Key Design Patterns

**Dual Provider System**:
- `OpenAIClient` (`internal/llm/openai.go`) - Works with OpenAI and any OpenAI-compatible endpoint
- `AnthropicClient` (`internal/llm/anthropic.go`) - Native Anthropic SDK integration
- Provider selection via `LLM_PROVIDER` environment variable
- Conditional config binding based on provider (see `internal/config/config.go:Load()`)

**Streaming Architecture**:
- Uses Go channels for real-time response streaming
- `StreamChunk` struct carries `Content` (main response), `Thinking` (reasoning), and `ToolUse` (tool requests)
- Anthropic: Native `thinking_delta` events from streaming API
- OpenAI-compatible: Delimiter-based extraction (`<thinking>`, `<reasoning>`, etc.)

**Thinking Display**: Model reasoning stored separately, rendered in a compact 2-line box. Parsed from `[Thinking]`/`[Response]` section markers via `renderAssistantContent()`.

**Conversation Tracing**: JSONL logs at `~/.config/stoa/traces/trace_YYYY-MM-DD_HH-MM-SS.jsonl`. Enabled by default; toggle via `trace.enabled`.

### File Structure

```
internal/
├── ui/           - Bubbletea UI (Model-Update-View); thin client mode via agentproto
├── agentproto/   - Shared WebSocket protocol types (Event, ClientMessage, SessionConfig)
├── agentdb/      - Postgres session/message store (used by agentd)
├── agents/       - Agent registry logic
├── agentskills/  - Skills/tools wired into agentd
├── llm/          - LLM client interface and implementations (used by agentd only)
├── chat/         - Message types and utilities
├── config/       - Viper configuration with provider-specific binding
├── compaction/   - Auto-compaction system
├── skills/       - Command/tool registry system
├── mcp/          - MCP client implementation
├── mcpserver/    - MCP server framework
├── channels/     - Multi-channel abstraction (Zulip, Slack, Discord, Matrix, etc.)
├── bridges/      - Shared bridge logic (used by channel bridge binaries)
├── zulip/        - Zulip API client
├── slack/        - Slack API client
├── discord/      - Discord API client
├── matrix/       - Matrix API client
├── proxy/        - LLM proxy (OpenAI-compatible passthrough)
├── permissions/  - Permission check/enforcement layer
├── ratelimit/    - Rate limiting
├── db/           - Shared DB utilities
├── oauth/        - OAuth2 helpers
├── logging/      - Structured logging setup
├── trace/        - Conversation tracing system
├── authdb/       - Postgres user/session store (used by authd)
├── authd/        - OAuth2 identity broker server
├── authdclient/  - Thin HTTP client for validating tokens against authd
├── localidp/     - Local OIDC identity provider (username/password, argon2id)
├── apikeyd/      - API key daemon
├── apikeydclient/- Thin client for apikeyd
└── webd/         - Web UI BFF (cookie auth, WebSocket proxy, embedded Svelte SPA)

cmd/
├── chat/          - TUI client (standalone or remote mode via AGENT_ADDRESS)
├── agentd/        - Agent daemon — LLM, MCP, tools, sessions over WebSocket
├── authd/         - Auth daemon — OAuth2 broker, token issuance, session management
├── local-idp/     - Local OIDC provider for dev (and production-ready)
├── webd/          - Web UI server (serves Svelte SPA, proxies to agentd)
├── auth-admin/    - Admin CLI: DB migrations, user management, service tokens
├── zulip-bridge/  - Zulip ↔ agentd bridge
├── slack-bridge/  - Slack ↔ agentd bridge
├── discord-bridge/- Discord ↔ agentd bridge
├── matrix-bridge/ - Matrix ↔ agentd bridge
├── cli-bridge/    - CLI bridge
├── llm-proxy/     - OpenAI-compatible LLM proxy
├── mcp-acp/       - ACP MCP server
├── apikeyd/       - API key service binary
├── dump-context/  - Debug tool: dump agentd session context
└── mcp-*/         - Standalone MCP servers (filesystem, memory, fetch, time, diff, web-search, http, shell, trello, zulip, netbox, promql, graphql)

web/              - Svelte 5 + Vite frontend (built into internal/webd/dist/ for go:embed)

docs/
├── AGENTD_REFACTOR.md - Architecture design for agentd split
└── remaining/         - Remaining work (README + SECURITY + MCP_SERVERS + PROXY_COMPLIANCE)
```

## Provider Configuration

**Anthropic**:
```bash
export LLM_PROVIDER="anthropic"
export ANTHROPIC_API_KEY="sk-ant-..."
export ANTHROPIC_API_URL="https://api.anthropic.com"  # Optional
export ANTHROPIC_MODEL="claude-sonnet-4-5-20250929"   # Optional; this is the compiled-in default (may be outdated)
```

**OpenAI** (or compatible endpoints):
```bash
export LLM_PROVIDER="openai"  # or omit, it's the default
export OPENAI_API_KEY="sk-..."
export OPENAI_API_URL="https://api.openai.com/v1"  # Optional
export OPENAI_MODEL="gpt-4o"                        # Optional
```

For other OpenAI-compatible endpoints (Together, Groq, Ollama), change `OPENAI_API_URL`.

## Common Gotchas

1. **Provider-specific env var binding**: Cannot bind both `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` to `llm.api_key`. Use conditional binding based on provider.

2. **Tool calling implementation**: Both OpenAI and Anthropic clients fully support tool calling. The `convertToolsToOpenAI()` and `convertToolsToAnthropic()` functions handle SDK-specific type conversions. Tools registered in the skills registry are automatically available to the LLM.

3. **Tool calling flow**: Complete sequence is: register tools in registry → pass to StreamMessage() → detect ToolUse in StreamChunk → execute via registry → add result as user message → send streamStartMsg to continue conversation.

4. **WebSocket library mismatch**: agentd serves WebSocket with `golang.org/x/net/websocket`. Any client dialing agentd (including `webd`'s proxy) must also use `x/net/websocket` — gorilla/websocket's dialer fails the x/net handshake silently from the browser's perspective.

5. **local-idp dual URLs**: `LOCAL_IDP_ISSUER` (internal container name, used by authd for OIDC discovery and token/userinfo calls) must differ from `LOCAL_IDP_EXTERNAL_URL` (browser-reachable localhost URL, used only for `authorization_endpoint` in the discovery doc). Setting them the same breaks either authd's discovery or the browser's ability to reach the login form.

6. **`podman-compose restart` does not re-read `.env`**: It restarts the existing container with its original environment. To pick up `.env` changes you must recreate the container: `podman-compose up -d --force-recreate <service>`.

7. **podman-compose `${VAR:-default}` not supported**: Older podman-compose versions pass the literal string through. Always set explicit values in `.env`; use bare `${VAR}` in compose files.

8. **`AUTHD_ALLOWED_REDIRECT_ORIGINS` is comma-separated**: `parseAllowedOrigins` splits on `,` not whitespace. A space-separated list is treated as one entry and every origin fails the allowlist check with "post_login_redirect origin not in allowlist".

9. **podman-compose `service_completed_successfully` not supported**: The `condition: service_completed_successfully` dependency type causes a "not found in input list" container graph error. Use `condition: service_healthy` against the database directly instead. Services that auto-migrate on startup (like `apikeyd`) don't need to depend on a separate migration container at all — the one-shot migrate containers run independently with `restart: on-failure`.

10. **Large MCP tool results (context flooding)**: Introspection-style MCP tools (e.g., `netbox_introspect`) can return hundreds of kilobytes, filling the entire context window. agentd caps tool results at **50KB** (`maxToolResultBytes` in `cmd/agentd/toolpager.go`). Oversized results are stored in `sess.resultCache` and the LLM is given page 1 plus a notice to call the auto-registered `read_tool_result` per-session tool for subsequent pages. **The correct fix is in the tool**: MCP tools should return summaries/indexes by default and accept filter/pagination parameters. See `docs/INEFFICIENCIES.md` for the full antipattern analysis.

## Testing

```bash
# Test Anthropic
make build && LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat

# Test OpenAI
make build && LLM_PROVIDER=openai OPENAI_API_KEY=sk-... ./bin/debug/chat

# Test with custom endpoint (e.g., Ollama)
make build && OPENAI_API_URL=http://localhost:11434/v1 OPENAI_API_KEY=ollama OPENAI_MODEL=llama3.2 ./bin/debug/chat

# Test MCP filesystem server manually
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test","version":"1.0"}}}' | ./bin/debug/mcp-filesystem -root /tmp
```

---

## Quick Reference

**Bubbletea Message Types** (`internal/ui/messages.go`):
- `streamStartMsg` - Triggers streaming state (standalone mode)
- `streamChunkMsg` - Carries content/thinking/toolUse (standalone mode)
- `streamDoneMsg` - Ends streaming (standalone mode)
- `agentEventMsg` - Carries agentproto.Event from agentd (remote mode)
- `agentConnectedMsg` / `agentDisconnectedMsg` - Connection lifecycle (remote mode)
- `compactionNeededMsg` / `compactionDoneMsg` - Compaction flow
- `commandExecutedMsg` - Command result
- `toolResultMsg` - Tool execution result

**agentproto Event Types** (`internal/agentproto/events.go`):
- `EventTypeChunk` - Streaming content/thinking delta
- `EventTypeDone` - End of turn with authoritative full content
- `EventTypeToolInvoked` / `EventTypeToolResult` - Tool observability
- `EventTypePermRequest` - Client must respond via `perm_response` ClientMessage
- `EventTypePermMention` - FYI: tool ran silently
- `EventTypeCompaction` - History was compacted
- `EventTypeError` - Error; `Fatal: true` means session is over

**agentd WebSocket protocol**:
- `POST /sessions` → `{ "session_id": "..." }`
- `GET /sessions` → `[]SessionInfo`
- `WS /sessions/:id/ws` → bidirectional JSON frames
  - Client sends: `{ "type": "send_message", "send_message": { "content": "..." } }`
  - Client sends: `{ "type": "perm_response", "perm_response": { "request_id": "...", "approved": true } }`

**agentproto `SessionConfig.Source`**: Set to `"web"`, `"tui"`, `"zulip"`, `"slack"`, `"discord"`, `"matrix"`, or `"cli"` when creating sessions. Stored on the session and returned in `SessionInfo`. webd filters the sidebar to `source == "web"`.

**authd HTTP API**:
- `POST /oauth/login` → `{ "login_id", "url", "expires_in" }` — starts browser login flow
  - Body may include `"post_login_redirect": "https://..."` for BFF redirect (origin must be in `AUTHD_ALLOWED_REDIRECT_ORIGINS`)
- `GET /oauth/login/poll?id=<login_id>` → `202 {status:pending}` or `200 {session_token}`
- `POST /token/verify` (Bearer) → `{ user_id, email, name, expires_at }`
- `POST /token/service` (admin Bearer) → `{ session_token }` — service account token
- `GET /healthz` → `{"status":"ok"}`

**webd HTTP API**:
- `GET /auth/login` — starts OIDC browser flow
- `GET /auth/callback?token=` — receives token from authd, sets HttpOnly cookie
- `POST /auth/logout` — clears cookie
- `GET /api/me` — returns user info from authd (validates cookie)
- `GET|POST /api/sessions[/...]` — proxies to agentd with injected Bearer token
- `WS /ws/:sessionId` — proxies to agentd WebSocket with injected Bearer token

**auth-admin usage**:
```bash
auth-admin authd migrate [up|down|status|version]   # AUTHD_MIGRATE_DATABASE_URL
auth-admin authd user list
auth-admin authd user revoke <email>
auth-admin authd service create <id> [--ttl-hours N]

auth-admin idp migrate [up|down|status]             # LOCAL_IDP_MIGRATE_DATABASE_URL
auth-admin idp user list
auth-admin idp user create --username U --email E --password P
auth-admin idp user reset-password --username U --password P
auth-admin idp user unlock --username U
auth-admin idp user delete --username U
```

**TUI remote mode**: Set `AGENT_ADDRESS=http://host:port` (or `agent.address` in config). If set and no LLM key, TUI skips LLM validation and connects to agentd.

**Critical Configuration Logic** (`internal/config/config.go:Load()`):
1. Binds `LLM_PROVIDER` environment variable first
2. Based on provider, conditionally binds provider-specific env vars
3. Sets provider-specific defaults (URLs, models)
4. **Important**: Cannot use multiple `BindEnv()` calls on same config key - only last one takes effect
5. If `agent.address` is set and `llm.api_key` is empty, LLM validation is skipped (remote mode)

**Agent Registry** (`/agents/` and `/acp/`):
- `GET /agents` / `POST /agents` — list or create agents
- `GET /agents/{name}` / `PUT /agents/{name}` / `DELETE /agents/{name}` — manage agents
- `POST /agents/{name}/publish` / `POST /agents/{name}/unpublish` — control ACP visibility
- `GET /acp/ping` — health (no auth)
- `GET /acp/agents` — list published agents
- `POST /acp/runs` — create a run (`mode`: `sync`/`async`/`stream`)
- `GET /acp/runs/{id}` — poll status; `POST /acp/runs/{id}` — resume (perm response)
- `POST /acp/runs/{id}/cancel` — cancel run

```bash
# auth-admin agentd agent commands (requires AGENTD_DATABASE_URL)
auth-admin agentd agent list
auth-admin agentd agent create --name my-agent --display-name "My Agent" --visibility external
auth-admin agentd agent publish --name my-agent
auth-admin agentd agent unpublish --name my-agent
auth-admin agentd agent delete --name my-agent
```

**Message Filtering in `streamFromLLM()`**:
- Filter out system messages (not sent to LLM)
- Filter out summary messages (display only)
- Filter out the empty streaming placeholder message
- Validate sequence ends with user message

