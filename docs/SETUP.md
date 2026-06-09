# Setup

## Prerequisites

- Go 1.22+
- An LLM API key (Anthropic or OpenAI-compatible)
- Podman + podman-compose (for containerized mode only)

---

## Mode 1: Standalone TUI (simplest)

The TUI runs with an embedded LLM — no daemon needed.

Provider and model are set in `~/.config/stoa/config.yaml` under `llm.providers` (see `CONTRIBUTING.md` for the full YAML schema). The API key comes from an environment variable.

```bash
make build-chat

# Anthropic (config.yaml must have an entry with type: anthropic)
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/chat

# OpenAI or compatible (config.yaml must have an entry with type: openai)
OPENAI_API_KEY=sk-... ./bin/debug/chat
```

---

## Mode 2: agentd + TUI (recommended)

The daemon owns all LLM/MCP/tool logic; the TUI is a thin client.

```bash
make build-agentd build-chat

# 1. Start agentd (provider configured in config.yaml)
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/agentd --port 7777

# 2. Connect TUI (no API key needed on client)
AGENT_ADDRESS=http://localhost:7777 ./bin/debug/chat
```

For persistent config, set `agent.address` in `~/.config/stoa/config.yaml`.

---

## Mode 3: Containerized (Podman)

Runs agentd, MCP servers, auth stack, and web UI in containers.

The fastest path is the setup wizard, which generates `.env` with all secrets
pre-filled and prints the exact migration commands to run:

```bash
./scripts/init.sh   # choose option 3
```

Or do it manually:

```bash
# 1. Copy and fill in env
cp .env.example .env
# Edit .env — generate secrets with: openssl rand -hex 32
# Store API keys as Podman secrets: ./scripts/setup-secrets.sh

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
