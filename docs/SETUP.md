# Setup

## Prerequisites

- Go 1.22+
- An LLM API key (Anthropic or OpenAI-compatible)
- Podman + podman-compose (for containerized mode only)

---

## Mode 1: Standalone TUI (simplest)

The TUI runs with an embedded LLM — no daemon needed.

```bash
make build-chat

# Anthropic
LLM_PROVIDER=anthropic ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat

# OpenAI or compatible
OPENAI_API_KEY=sk-... ./bin/debug/chat
```

---

## Mode 2: agentd + TUI (recommended)

The daemon owns all LLM/MCP/tool logic; the TUI is a thin client.

```bash
make build-agentd build-chat

# 1. Start agentd
ANTHROPIC_API_KEY=sk-ant-... LLM_PROVIDER=anthropic ./bin/debug/agentd --port 7777

# 2. Connect TUI (no API key needed on client)
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat
```

For persistent config, set `agent.address` in `~/.config/stoa/config.yaml`.

---

## Mode 3: Containerized (Podman)

Runs agentd, MCP servers, auth stack, and web UI in containers.

```bash
# 1. Copy and fill in env
cp .env.example .env
# Edit .env — at minimum set ANTHROPIC_API_KEY or OPENAI_API_KEY

# 2. Build images
make podman-build-all

# 3. Start services
podman-compose up -d

# 4. Connect TUI from host
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat
# Or open the web UI at http://localhost:8080
```

See [CONTAINERIZATION.md](CONTAINERIZATION.md) for full container reference and [PODMAN.md](PODMAN.md) for Podman-specific setup.

---

## Zulip Bridge

```bash
make build-zulip-bridge

AGENT_ADDRESS=http://localhost:7777 \
ZULIP_EMAIL=bot@example.com \
ZULIP_API_KEY=... \
ZULIP_SITE=https://your.zulip.com \
./bin/debug/zulip-bridge
```

See [ZULIP_SERVER.md](ZULIP_SERVER.md) for full Zulip setup.
