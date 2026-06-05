# Stoa

> [!WARNING]
> This project is under active development.  Not recommended for production use without thorough testing.

An AI agent platform built around a central daemon (`agentd`) that owns all LLM interaction, tool execution, session management, and MCP integration. Clients — a terminal UI, a Svelte web app, or any of the chat platform bridges — connect to agentd over WebSocket and receive a real-time event stream.

## Architecture

```

                                     Messaging Bridges
        TUI         WebUI       (Slack, Zulip, Matrix, etc.)
         │            │                       │
         └────────────┴───────────────────────┘
                             │
                   WebSocket (agentproto)
                             │
                 ┌───────────▼───────────┐
                 │       agentd          │
                 │                       │
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
            │               │  └─────┬─────┘
            │ filesystem    │        │ OIDC
            │ memory        │   ┌────▼────┐
            │ shell         │   │local-idp│
            │ etc.          │   └─────────┘
            └───────────────┘

```

### Components

* **agentd** — The agent daemon. Runs the LLM conversation loop, dispatches MCP tool calls, enforces the permission system, persists sessions to Postgres, streams events to connected clients, and exposes an ACP-compatible HTTP endpoint for agent orchestration.

* **chat** — A [Bubble Tea](https://github.com/charmbracelet/bubbletea) terminal UI. Runs standalone (embedded LLM, no daemon) or in remote mode by pointing `AGENT_ADDRESS` at a running agentd instance. All clients implement the Elm architecture (Model-Update-View).

* **webd** — An HTTP server that serves a Svelte 5 single-page app and proxies API and WebSocket traffic to agentd behind cookie-based authentication.

* **authd / local-idp** — An OAuth2 identity broker with a local OIDC provider (username/password, argon2id). `authd` handles token issuance and session management; `local-idp` is the identity backend.

* **Bridges** — Standalone processes that translate between agentd and external chat platforms: Zulip, Slack, Discord, Matrix, and a generic CLI bridge.

* **MCP servers** — Built-in tool servers implementing the [Model Context Protocol](https://modelcontextprotocol.io): filesystem, memory, fetch, shell, diff, web-search, HTTP, GraphQL, PromQL, NetBox, Trello, Zulip, and an ACP MCP server.

* **inference-gateway** — An OpenAI-compatible passthrough proxy with API key management via `apikeyd`.

## LLM Providers

Provider selection and model are configured in YAML (`~/.config/stoa/config.yaml`) under `llm.providers`. API keys and endpoint URLs come from environment variables.

| Provider | Env vars | Notes |
|---|---|---|
| Anthropic | `ANTHROPIC_API_KEY` | Native streaming, extended thinking |
| OpenAI | `OPENAI_API_KEY` | Default |
| Any OpenAI-compatible | `OPENAI_API_URL`, `OPENAI_API_KEY` | Together, Groq, Ollama, etc. |

## Features

* **Streaming with thinking** — Anthropic thinking deltas and OpenAI-style `<thinking>` / `<reasoning>` delimiters are parsed out of the stream and displayed in a compact 2-line preview, separate from the main response.

* **Auto-compaction** — When a conversation approaches a configurable token threshold, agentd (or the standalone TUI) summarizes older messages in place and continues. Configurable threshold, strategy, and how many recent messages to preserve verbatim.

* **Permission system** — Every MCP tool call is classified by rank: silent (runs automatically), mention (logged but not blocked), or request (requires explicit client approval before execution). Ranks are configured per tool pattern in `config/permissions-default.yaml` and can be overridden per server.

* **Sub-agents** — agentd can spawn child sessions with their own LLM context, persona, and tool access. The `spawn_agent`, `message_agent`, and `cancel_agent` built-in tools allow the primary agent to delegate work.

* **ACP endpoint** — agentd exposes an Agent Control Protocol HTTP API (`/acp/`) for external orchestration: listing published agents, creating synchronous or streaming runs, polling status, and responding to permission requests.

* **Conversation tracing** — Every session writes a timestamped JSONL trace file capturing all messages, tool calls, and tool results. Enabled by default; configurable location.

* **Skills system** — Slash commands (`/help`, `/clear`, `/model`, `/compact`, `/spawn`, …) and LLM-accessible tools are registered through a shared registry that works identically in standalone and daemon modes.

## Getting Started

See [docs/SETUP.md](docs/SETUP.md) for full installation and configuration instructions. The short version:

```bash
# Standalone TUI — requires a config.yaml with llm.providers (see docs/SETUP.md)
make build-chat
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat

# Daemon + TUI (recommended)
make build-agentd build-chat
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/agentd --port 7777
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat

# Full stack in containers (agentd, webd, auth, MCP servers)
./scripts/init.sh      # guided setup: generates .env, secrets, and next-step commands
make podman-build-all
podman-compose up -d
```

## Documentation

- [docs/SETUP.md](docs/SETUP.md) — Installation and configuration
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — Detailed architecture notes
- [docs/PERMISSIONS.md](docs/PERMISSIONS.md) — Permission system reference
- [docs/CONTAINERIZATION.md](docs/CONTAINERIZATION.md) — Container and Podman reference
- [docs/ZULIP_SERVER.md](docs/ZULIP_SERVER.md) — Zulip bridge setup
- [docs/SLACK_BRIDGE.md](docs/SLACK_BRIDGE.md) — Slack bridge setup
- [docs/DISCORD_BRIDGE.md](docs/DISCORD_BRIDGE.md) — Discord bridge setup
- [docs/MATRIX_BRIDGE.md](docs/MATRIX_BRIDGE.md) — Matrix bridge setup
- [ROADMAP.md](ROADMAP.md) — Outstanding work: performance, security, gateway, MCP servers, bridge features

## Building

```bash
make build          # all binaries → bin/debug/
make build-chat     # TUI client only
make build-agentd   # daemon only
make build-mcp-all  # all MCP servers
make dist           # stripped release builds → bin/dist/
make test           # run tests
```

Container images (requires Docker or Podman):

```bash
make podman-build-all        # build all images tagged :local
make podman-push-all         # retag and push to ghcr.io/stanier
```

---

Donations are welcome — see [docs/DONATIONS.md](docs/DONATIONS.md) if you'd like to contribute.
