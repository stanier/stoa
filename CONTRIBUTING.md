# Contributing to Stoa

Thanks for your interest in contributing. This document covers how to get set up, how to run tests, and how pull requests are handled.

## Prerequisites

- Go 1.22+ (version pinned in `go.mod`)
- Node.js 20+ (for the Svelte web frontend in `web/`)
- Make
- An LLM API key (Anthropic or OpenAI) if you want to test end-to-end

Optional but useful for container-based features:
- Podman + podman-compose

## Building

```bash
make build              # all binaries → bin/debug/
make build-chat         # TUI client only
make build-agentd       # agent daemon only
make build-zulip-bridge # Zulip bridge only
make build-mcp-all      # all MCP server binaries
make dev                # build with race detector
make clean              # remove build artifacts
make fmt                # format code (gofmt + goimports)
```

The `web/` frontend is embedded into the `webd` binary at build time via `go:embed`. `make build` handles the `npm ci && npm run build` step automatically.

## Running tests

```bash
make test
```

Tests are pure Go unit tests — no LLM key or running daemon required. The CI workflow runs `make test` and `make build` on every PR against `main`.

## Running locally

### Provider configuration

Provider selection, model, and titling options live in the YAML config (`~/.config/stoa/config.yaml`). Credentials and endpoint URLs go in environment variables (or `.env`).

Providers must be defined as a list under `llm.providers`. The flat keys `llm.provider` and `llm.model` do not exist and are silently dropped.

**Anthropic:**
```yaml
llm:
  providers:
    - name: anthropic
      type: anthropic
      model: claude-sonnet-4-5-20250929
      max_tokens: 8000
      api_key_env: ANTHROPIC_API_KEY
```
```bash
ANTHROPIC_API_KEY=sk-ant-...
```

**OpenAI (or compatible endpoints — Together, Groq, Ollama):**
```yaml
llm:
  providers:
    - name: openai
      type: openai
      model: gpt-4o
      max_tokens: 4096
      api_key_env: OPENAI_API_KEY
      # api_url_env: OPENAI_API_URL  # omit for the public OpenAI endpoint
```
```bash
OPENAI_API_KEY=sk-...
```

If `api_key_env`/`api_url_env` are omitted, the loader falls back to `ANTHROPIC_API_KEY`/`ANTHROPIC_API_URL` (or `OPENAI_*`) automatically.

### Standalone TUI (embedded LLM, no daemon)

```bash
make build
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat
# or
OPENAI_API_KEY=sk-... ./bin/debug/chat
# or with a custom endpoint (e.g., Ollama — set api_url_env in YAML)
OPENAI_API_KEY=ollama OPENAI_API_URL=http://localhost:11434/v1 ./bin/debug/chat
```

### Remote mode (TUI → agentd)

```bash
# 1. Start daemon
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/agentd --port 7777
# 2. Connect TUI (no LLM key needed on the client)
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat
```

Set `AGENT_ADDRESS=http://host:port` (or `agent.address` in config). If set and no LLM key is present, the TUI skips LLM validation and connects directly.

### Bridges

```bash
# Zulip bridge
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/zulip-bridge
```

### Testing MCP servers manually

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test","version":"1.0"}}}' | ./bin/debug/mcp-filesystem -root /tmp
```

### Container stack

See `docs/SETUP.md` and `docs/PODMAN.md` for the full podman-compose setup.

## Architecture

`agentd` is the core daemon: it owns LLM interaction, MCP dispatch, tool execution, session persistence, and the ACP endpoint. All clients connect to it over WebSocket using the `agentproto` protocol and receive a real-time event stream.

```
                                 Messaging Bridges
      TUI         WebUI     (Slack, Zulip, Matrix, etc.)
       │            │                   │
       └────────────┴───────────────────┘
                         │
               WebSocket (agentproto)
                         │
             ┌───────────▼───────────┐
             │         agentd        │
             │  LLM streaming        │
             │  MCP dispatch         │
             │  Permission system    │
             │  Session persistence  │
             │  Sub-agent runtime    │
             │  ACP endpoint         │
             └──┬────────────────┬───┘
                │                │
           stdio/docker     auth tokens
                │                │
        ┌───────▼───────┐  ┌─────▼─────┐
        │  MCP Servers  │  │   authd   │
        └───────────────┘  └─────┬─────┘
                                 │ OIDC
                            ┌────▼────┐
                            │local-idp│
                            └─────────┘
```

All clients follow the Elm Architecture (Model-Update-View) and share the `agentproto` WebSocket protocol.

### Key design patterns

**Dual provider system** — two LLM client implementations:
- `OpenAIClient` (`internal/llm/openai.go`) — works with OpenAI and any OpenAI-compatible endpoint
- `AnthropicClient` (`internal/llm/anthropic.go`) — native Anthropic SDK integration
- Provider selection via YAML config; conditional env binding in `internal/config/config.go:Load()`

**Streaming architecture** — Go channels carry real-time response chunks:
- `StreamChunk` struct carries `Content` (main response), `Thinking` (reasoning), and `ToolUse` (tool requests)
- Anthropic: native `thinking_delta` events from the streaming API
- OpenAI-compatible: delimiter-based extraction (`<thinking>`, `<reasoning>`, etc.)

**Tool calling flow**: register tools in registry → pass to `StreamMessage()` → detect `ToolUse` in `StreamChunk` → execute via registry → add result as user message → send `streamStartMsg` to continue conversation. Both providers use `convertToolsToOpenAI()` / `convertToolsToAnthropic()` for SDK-specific type conversion.

**Thinking display**: model reasoning stored separately, rendered in a compact 2-line box. Parsed from `[Thinking]`/`[Response]` section markers via `renderAssistantContent()`.

**Conversation tracing**: JSONL logs at `~/.config/stoa/traces/trace_YYYY-MM-DD_HH-MM-SS.jsonl`. Enabled by default; toggle via `trace.enabled`.

## Codebase layout

```
internal/
├── ui/             - Bubbletea TUI (Model-Update-View); thin client via agentproto
├── agentproto/     - Shared WebSocket protocol types (Event, ClientMessage, SessionConfig)
├── agentdb/        - Postgres session/message store (agentd)
├── agents/         - Agent registry logic
├── agentskills/    - Skills/tools wired into agentd
├── llm/            - LLM client interface and implementations (agentd only)
├── chat/           - Message types and utilities
├── config/         - Viper configuration with provider-specific binding
├── compaction/     - Auto-compaction system
├── skills/         - Command/tool registry system
├── mcp/            - MCP client implementation
├── mcpserver/      - MCP server framework
├── channels/       - Multi-channel abstraction (Zulip, Slack, Discord, Matrix, etc.)
├── bridges/        - Shared bridge logic (used by channel bridge binaries)
├── zulip/          - Zulip API client
├── slack/          - Slack API client
├── discord/        - Discord API client
├── matrix/         - Matrix API client
├── inferencegateway/ - OpenAI-compatible passthrough
├── permissions/    - Permission check/enforcement layer
├── ratelimit/      - Rate limiting
├── db/             - Shared DB utilities
├── oauth/          - OAuth2 helpers
├── logging/        - Structured logging setup
├── trace/          - Conversation tracing system
├── authdb/         - Postgres user/session store (authd)
├── authd/          - OAuth2 identity broker server
├── authdclient/    - Thin HTTP client for validating tokens against authd
├── localidp/       - Local OIDC identity provider (username/password, argon2id)
├── apikeyd/        - API key daemon
├── apikeydclient/  - Thin client for apikeyd
└── webd/           - Web UI BFF (cookie auth, WebSocket proxy, embedded Svelte SPA)

cmd/
├── chat/           - TUI client (standalone or remote via AGENT_ADDRESS)
├── agentd/         - Agent daemon — LLM, MCP, tools, sessions over WebSocket
├── authd/          - Auth daemon — OAuth2 broker, token issuance, session management
├── local-idp/      - Local OIDC provider (dev and production-ready)
├── webd/           - Web UI server (serves Svelte SPA, proxies to agentd)
├── auth-admin/     - Admin CLI: DB migrations, user management, service tokens
├── zulip-bridge/   - Zulip ↔ agentd bridge
├── slack-bridge/   - Slack ↔ agentd bridge
├── discord-bridge/ - Discord ↔ agentd bridge
├── matrix-bridge/  - Matrix ↔ agentd bridge
├── cli-bridge/     - CLI bridge
├── inference-gateway/ - OpenAI-compatible LLM proxy
├── mcp-acp/        - ACP MCP server
├── apikeyd/        - API key service binary
├── dump-context/   - Debug tool: dump agentd session context
└── mcp-*/          - Standalone MCP servers (filesystem, memory, fetch, time, diff,
                      web-search, http, shell, trello, zulip, netbox, promql, graphql)

web/                - Svelte 5 + Vite frontend (embedded into webd via go:embed)

docs/               - Architecture and design documentation
```

## Common pitfalls

1. **Provider/model config is YAML-only.** `LLM_PROVIDER`, `ANTHROPIC_MODEL`, `OPENAI_MODEL`, and `TITLING_ENABLED` are not read from environment variables. Define providers under `llm.providers` (a list) and the `titling:` block in YAML. The flat keys `llm.provider` and `llm.model` do not exist and are silently dropped by mapstructure.

2. **WebSocket library mismatch.** agentd serves WebSocket with `golang.org/x/net/websocket`. Any client dialing agentd (including `webd`'s proxy) must also use `x/net/websocket` — gorilla/websocket's dialer fails the handshake silently from the browser's perspective.

3. **local-idp dual URLs.** `LOCAL_IDP_ISSUER` (internal container name, used by authd for OIDC discovery) must differ from `LOCAL_IDP_EXTERNAL_URL` (browser-reachable URL, used only for `authorization_endpoint` in the discovery doc). Setting them the same breaks either authd's discovery or the browser's login redirect.

4. **`podman-compose restart` does not re-read `.env`.** It restarts the existing container with its original environment. To pick up `.env` changes: `podman-compose up -d --force-recreate <service>`.

5. **podman-compose `${VAR:-default}` not supported.** Older podman-compose versions pass the literal string through. Always set explicit values in `.env`; use bare `${VAR}` in compose files.

6. **`AUTHD_ALLOWED_REDIRECT_ORIGINS` is comma-separated.** `parseAllowedOrigins` splits on `,` not whitespace. A space-separated list is treated as one entry and every origin fails the check.

7. **podman-compose `service_completed_successfully` not supported.** Causes a "not found in input list" container graph error. Use `condition: service_healthy` against the database directly. Services that auto-migrate on startup don't need to depend on a separate migration container.

8. **Large MCP tool results.** agentd caps tool results at 50KB (`maxToolResultBytes` in `cmd/agentd/toolpager.go`). Oversized results are paged via an auto-registered `read_tool_result` tool. The correct fix is in the tool itself: return summaries/indexes by default and accept filter/pagination parameters.

## Making a change

1. Fork the repo and create a branch off `main`.
2. Keep changes focused — one logical change per PR.
3. Run `make fmt` before pushing (`gofmt` + `goimports`).
4. Run `make test` and confirm it passes locally.
5. Open a pull request against `main`. The PR template will prompt for a summary and test plan.

For significant new features or architectural changes, open an issue first to discuss the approach before investing time in an implementation.

## Commit style

We use conventional commits loosely:

```
feat: add matrix bridge support
fix: handle empty tool result from MCP server
refactor: extract session store interface
docs: update provider configuration examples
test: add streaming chunk reassembly tests
```

Keep the subject line under 72 characters. Body is optional but appreciated for non-obvious changes.

## Code style

- Standard `gofmt` / `goimports` formatting — `make fmt` handles this.
- No comments that restate what the code does; comments are for non-obvious invariants or constraints.
- Error strings lowercase, no trailing punctuation (Go convention).
- New packages should follow the patterns in nearby packages. When in doubt, ask in the issue or PR.

## Reporting bugs

Use the bug report issue template. Include Go version, OS, provider type, and a minimal reproduction if possible. Logs from `~/.config/stoa/traces/` (with credentials redacted) are often helpful.
