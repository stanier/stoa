# Multi-Channel Chat Server

> **DEPRECATED:** This document describes `cmd/server`, which is superseded by the `cmd/agentd` + `cmd/zulip-bridge` architecture. See [docs/AGENTD_REFACTOR.md](AGENTD_REFACTOR.md) for the current design. The legacy content below is preserved for reference.

---

## Current Architecture (agentd + zulip-bridge)

The current approach separates the LLM daemon from the chat platform connector:

```
┌─────────────┐   WebSocket   ┌──────────────┐   Zulip API   ┌────────┐
│   agentd    │◄─────────────►│ zulip-bridge │◄─────────────►│  Zulip │
│  :7777      │               │              │               └────────┘
└─────────────┘               └──────────────┘
  LLM, MCP, tools               Zulip translator
  sessions, permissions
```

**agentd** owns all LLM/MCP/tool logic and exposes a WebSocket API.
**zulip-bridge** is a standalone process that translates between Zulip and agentd.

### Running the current stack

```bash
# 1. Start agentd
OPENAI_API_KEY=sk-... ./bin/debug/agentd --port 7777

# 2. Start zulip-bridge (in a separate terminal)
AGENT_ADDRESS=http://localhost:7777 \
ZULIP_SITE=https://your.zulip.server \
ZULIP_EMAIL=bot@example.com \
ZULIP_API_KEY=... \
./bin/debug/zulip-bridge
```

See [docs/AGENTD_REFACTOR.md](AGENTD_REFACTOR.md) for full protocol documentation and `cmd/zulip-bridge/bridge.go` as the reference implementation for new platform bridges.

---

## Legacy: cmd/server (Deprecated)

A headless daemon that integrates LLM capabilities with various chat platforms.

## Overview

The chat server (`server` binary) provides the same conversational AI features as the chat TUI, but operates as a daemon integrated with chat platforms like Zulip, Slack, Discord, and more. It listens for messages, processes them through the LLM, and sends responses back.

**Currently Supported Channels:**
- ✅ **Zulip** - Fully implemented
- 🚧 **Slack** - Coming soon
- 🚧 **Discord** - Coming soon
- 🚧 **Matrix** - Coming soon

The architecture is designed to be channel-agnostic, making it easy to add new chat platforms.

## Features

- **Multi-Channel Support**: Extensible architecture for multiple chat platforms
- **Headless Operation**: Runs as a daemon without UI
- **Full LLM Integration**: Supports both OpenAI and Anthropic providers
- **Tool Calling**: Built-in tools (calculator) available to the LLM
- **Channel-Specific Features**: Reaction tracking (Zulip), thread support, etc.
- **Thinking Display**: Model reasoning shown appropriately per channel
- **Graceful Shutdown**: Handles SIGINT/SIGTERM properly
- **Conversation Memory**: Maintains conversation history per session

## Architecture

The server uses a `Channel` interface to abstract different chat platforms:

```go
type Channel interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    IsConnected() bool
    Messages() <-chan Message
    SendResponse(originalMsg Message, response string, err error) error
    Name() string
}
```

Each platform implements this interface:
- `ZulipChannel` - Wraps the Zulip bridge
- `SlackChannel` - Coming soon
- `DiscordChannel` - Coming soon

The server code is completely channel-agnostic and works with any implementation.

## Build

```bash
make build-server
```

This creates the `server` binary in the project root.

## Configuration

### Environment Variables

```bash
# Zulip Configuration (Required)
export ZULIP_ENABLED=true
export ZULIP_SITE_URL=https://chat.example.com
export ZULIP_EMAIL=bot@example.com
export ZULIP_API_KEY=your_api_key_here

# LLM Provider (Required)
export LLM_PROVIDER=anthropic  # or "openai"

# Anthropic (if using Anthropic)
export ANTHROPIC_API_KEY=sk-ant-xxx
export ANTHROPIC_MODEL=claude-sonnet-4-5-20250929  # optional

# OpenAI (if using OpenAI)
export OPENAI_API_KEY=sk-xxx
export OPENAI_MODEL=gpt-4o  # optional
```

### Config File

Alternatively, use `~/.config/stoa/config.yaml`:

```yaml
llm:
  provider: anthropic
  api_key: sk-ant-xxx
  model: claude-sonnet-4-5-20250929

zulip:
  enabled: true
  site_url: https://chat.example.com
  email: bot@example.com
  api_key: your_api_key_here
  event_types:
    - message
  use_reactions: true
  emergency_cutoff: "!stop"  # optional safety phrase

logging:
  level: info
  log_file: ~/.config/stoa/server.log
```

## Usage

### Basic Usage

```bash
./server
```

The server will:
1. Connect to Zulip
2. Listen for messages
3. Process them through the LLM
4. Send responses back

### Command-Line Options

```bash
./server --help                      # Show help
./server --log-level debug           # Enable debug logging
./server --log-file /var/log/bot.log # Custom log file
```

### Stopping the Server

Press `Ctrl+C` or send `SIGTERM`:

```bash
kill -TERM $(pidof server)
```

## Bot Setup in Zulip

1. **Create a Bot User**:
   - Go to Settings → Personal settings → Bots
   - Click "Add a new bot"
   - Choose "Generic bot" type
   - Give it a name (e.g., "AI Assistant")
   - Copy the API key

2. **Subscribe to Streams**:
   - Add the bot to streams where it should respond
   - Or DM the bot directly for private conversations

3. **Permissions**:
   - The bot needs permission to read messages and send messages
   - Consider using a dedicated stream for testing

## Features in Detail

### Reaction Lifecycle

When enabled (`use_reactions: true`):

1. **👀 (eyes)**: Message received, processing started
2. **✏️ (pencil)**: Generating response
3. **(removed)**: Response sent

### Thinking Display

When the model uses reasoning (Claude extended thinking), it's formatted as a Zulip spoiler:

```
```spoiler Thinking
<model reasoning here>
```

<actual response>
```

### Tool Calling

Built-in tools are automatically available:

- **calculator**: Basic arithmetic operations (add, subtract, multiply, divide)

The LLM can invoke these tools as needed during conversation.

### Emergency Cutoff

Set `emergency_cutoff` to a safety phrase (e.g., "!stop") that will prevent the bot from responding to messages containing that phrase.

## Running as a System Service

### systemd Service

Create `/etc/systemd/system/zulip-bot.service`:

```ini
[Unit]
Description=Zulip LLM Bot
After=network.target

[Service]
Type=simple
User=botuser
WorkingDirectory=/home/botuser/chat-bot
Environment="ZULIP_ENABLED=true"
Environment="ZULIP_SITE_URL=https://chat.example.com"
Environment="ZULIP_EMAIL=bot@example.com"
Environment="ZULIP_API_KEY=xxx"
Environment="LLM_PROVIDER=anthropic"
Environment="ANTHROPIC_API_KEY=sk-ant-xxx"
ExecStart=/home/botuser/chat-bot/server --log-file /var/log/zulip-bot/server.log
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable zulip-bot
sudo systemctl start zulip-bot
sudo systemctl status zulip-bot
```

View logs:

```bash
sudo journalctl -u zulip-bot -f
```

## Logging

Logs are written to:
- **stderr**: Human-readable, colored output (when running interactively)
- **file**: JSON format (one object per line, good for parsing)

Default log location: `~/.config/stoa/server.log`

Query logs with `jq`:

```bash
cat ~/.config/stoa/server.log | jq 'select(.level=="error")'
cat ~/.config/stoa/server.log | jq 'select(.msg | contains("Received message"))'
```

## Troubleshooting

### Connection Issues

```bash
# Enable debug logging
./server --log-level debug

# Check Zulip credentials
curl -X GET https://chat.example.com/api/v1/users/me \
  -u "BOT_EMAIL:API_KEY"
```

### Rate Limiting

If you see "rate limited" errors:
- Reduce message volume
- The server already handles rate limits gracefully with backoff

### Memory Usage

The server maintains full conversation history. For long-running bots:
- Consider implementing conversation compaction (see PHASE1_COMPACTION.md)
- Or restart periodically to clear history

## Differences from TUI Client

| Feature | TUI (`chat`) | Server (`server`) |
|---------|-------------|------------------|
| UI | Bubbletea TUI | Headless daemon |
| Zulip | Optional | Required |
| Interactive | Yes | No (Zulip-only) |
| Display | Rich terminal | Logs only |
| Conversation | Per session | Persistent |
| Shutdown | Exit TUI | SIGTERM/SIGINT |

## Security Considerations

1. **API Keys**: Never commit API keys to git. Use environment variables or config files with proper permissions (`chmod 600`).

2. **Emergency Cutoff**: Set this to prevent runaway conversations or abuse.

3. **Rate Limiting**: The Zulip API has rate limits. The bot handles these with exponential backoff.

4. **Message Filtering**: The bot ignores its own messages to prevent loops.

5. **Logging**: Log files may contain sensitive conversation data. Rotate and secure them appropriately.

## Examples

### Development Testing

```bash
# Terminal 1: Run server with debug logs
ZULIP_ENABLED=true \
ZULIP_SITE_URL=https://chat.example.com \
ZULIP_EMAIL=bot@example.com \
ZULIP_API_KEY=xxx \
LLM_PROVIDER=anthropic \
ANTHROPIC_API_KEY=sk-ant-xxx \
./server --log-level debug

# Terminal 2: Watch logs
tail -f ~/.config/stoa/server.log | jq
```

### Production Deployment

```bash
# Build optimized binary
go build -ldflags="-s -w" -o server ./cmd/server

# Run with config file
./server --log-file /var/log/zulip-bot.log
```

## Future Enhancements

Potential improvements (not yet implemented):

- [ ] Conversation persistence across restarts
- [ ] Multi-stream support with separate conversation contexts
- [ ] MCP server integration
- [ ] Auto-compaction for long conversations
- [ ] Metrics/monitoring endpoint
- [ ] Admin commands via Zulip DM

## See Also

- [ZULIP_INTEGRATION.md](ZULIP_INTEGRATION.md) - Original Zulip integration docs
- [PHASE2_SKILLS.md](PHASE2_SKILLS.md) - Skills/tools system
- [CLAUDE.md](../CLAUDE.md) - Main project documentation
