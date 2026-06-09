# Zulip Bridge Setup

The Zulip bridge (`cmd/zulip-bridge`) is a standalone process that translates between Zulip events and the agentd WebSocket protocol.

```
┌─────────────┐   WebSocket   ┌──────────────┐   Zulip API   ┌────────┐
│   agentd    │◄─────────────►│ zulip-bridge │◄─────────────►│  Zulip │
│  :7777      │               │              │               └────────┘
└─────────────┘               └──────────────┘
  LLM, MCP, tools               Zulip translator
  sessions, permissions
```

**agentd** owns all LLM/MCP/tool logic.  
**zulip-bridge** translates Zulip events into agentd `send_message` frames and forwards agentd events back as Zulip messages.

## Running

```bash
make build-agentd build-zulip-bridge

# 1. Start agentd (provider configured in config.yaml)
ANTHROPIC_API_KEY=sk-ant-... ./bin/debug/agentd --port 7777

# 2. Start zulip-bridge
AGENT_ADDRESS=http://localhost:7777 \
ZULIP_SITE_URL=https://your.zulip.server \
ZULIP_EMAIL=bot@example.com \
ZULIP_API_KEY=... \
./bin/debug/zulip-bridge
```

Or in the containerized stack (see [SETUP.md](SETUP.md)):

```bash
podman-compose up -d
```

## Configuration

| Env var | Required | Description |
|---------|----------|-------------|
| `AGENT_ADDRESS` | Yes | agentd base URL (e.g. `http://localhost:7777`) |
| `ZULIP_SITE_URL` | Yes | Your Zulip server URL |
| `ZULIP_EMAIL` | Yes | Bot email address |
| `ZULIP_API_KEY` | Yes | Bot API key |
| `AGENTD_API_KEY` | If agentd auth enabled | Bearer token for agentd |

YAML config (`~/.config/stoa/config.yaml`) allows additional tuning:

```yaml
zulip:
  persona: default          # agentd persona to use
  session_ttl: 24h          # idle session expiry
  command_prefix: "!"       # prefix for commands
  allowed_streams: []       # streams to respond to freely (empty = all)
  use_reactions: true       # react with ✅/❌ on permission requests
```

## Permission requests

When agentd emits a `perm_request` event, the bridge posts a message to Zulip with ✅ and ❌ reaction options. React to approve or deny. The response is forwarded back to agentd as a `perm_response` ClientMessage.

## Reference implementation

`cmd/zulip-bridge/bridge.go` is the canonical reference for implementing new platform bridges.
