# Discord Bridge

The `discord-bridge` binary connects a Discord server to an `agentd` server,
following the same pattern as `zulip-bridge` and `slack-bridge`.

## Quick Start

```bash
# Generate a service token for the bridge (requires running authd)
auth-admin authd service create discord-bridge

# Run the bridge
AGENT_ADDRESS=http://localhost:7777 \
  AGENTD_API_KEY=<service-token> \
  AGENTD_AUTHD_URL=http://localhost:8888 \
  DISCORD_ENABLED=true \
  DISCORD_BOT_TOKEN=MTk4... \
  ./bin/debug/discord-bridge
```

## Discord App Setup

1. Go to <https://discord.com/developers/applications> and create a new application
2. Under **Bot**, add a bot user and copy the **Token**
3. Under **Bot → Privileged Gateway Intents**, enable:
   - **Message Content Intent** — required to read message text in DMs
4. Under **OAuth2 → URL Generator**, select scopes:
   - `bot`
   - Bot permissions: `Send Messages`, `Add Reactions`, `Read Message History`
5. Use the generated URL to invite the bot to your server

**Required Gateway Intents** (set automatically by the bridge):
- `DIRECT_MESSAGES` (1 << 12) — receive DM MessageCreate events
- `DIRECT_MESSAGE_REACTIONS` (1 << 13) — receive DM reaction events
- `MESSAGE_CONTENT` (1 << 15) — privileged; read message content

## Environment Variables

| Variable | Description |
|---|---|
| `DISCORD_ENABLED` | Set to `true` to enable |
| `DISCORD_BOT_TOKEN` | Bot token from Discord Developer Portal |
| `DISCORD_COMMAND_PREFIX` | Command prefix (default: `!`) |
| `DISCORD_PERSONA` | Default agentd persona (default: `default`) |
| `DISCORD_SESSION_TTL` | Idle session expiry (default: `24h`) |
| `DISCORD_SESSION_FILE` | Session persistence path |
| `AGENT_ADDRESS` | agentd server URL (default: `http://localhost:7777`) |
| `AGENTD_API_KEY` | Service token for agentd auth |
| `AGENTD_AUTHD_URL` | authd URL for account linking |

## Commands (DM only)

| Command | Description |
|---|---|
| `!link` | Link your Discord account to the auth system |
| `!unlink` | Remove your account link |
| `!status` | Show your account link status |
| `!clear` | Clear conversation history (saves a snapshot) |
| `!resume` | Restore the last cleared conversation |
| `!compact` | Compact conversation history |
| `!tokens` | Show token usage for the current conversation |
| `!model` | Show the current LLM provider and model |
| `!help` | Show available commands |

## Permission Requests

When a tool requires user approval, the bridge posts a message describing the
tool and its parameters, then adds ✅ and ❌ reactions. React with either to
approve or deny. Requests time out after 60 seconds (configurable via
`permissions.approval_timeout`).

**Note**: Discord uses the snowflake message ID (string) as the reaction target,
unlike Zulip (integer) and Slack (timestamp string). The `reactionIndex` is keyed
by `channelID:messageID`.

---

## Future Work

### Channel/Thread Handling

The MVP only handles DMs. The bridge requests `IntentDirectMessages` and
`IntentDirectMessageReactions`, so guild channel messages are not received.
Three options for the next iteration:

**Option A — Per-channel session (simplest)**
- All messages in a guild channel share one agentd session.
- Key: `guild:<guild_id>:channel:<channel_id>`
- Requires: `IntentGuildMessages`, `IntentGuildMessageReactions`, `IntentMessageContent`
- Pro: Simple. Con: Context bleeds across users.

**Option B — Per-thread session (recommended)**
- Each top-level message spawns a Discord thread; the bot replies in-thread.
- Thread replies route to the same agentd session.
- Key: `thread:<thread_id>`
- Requires: `IntentGuildMessages`, `GUILD_MESSAGE_REACTIONS`, `IntentMessageContent`
- Pro: Isolated context per conversation, natural Discord UX.
- Con: Requires creating threads on every new message.

**Option C — Per-user-per-channel session**
- Each (user, channel) pair gets its own session.
- Key: `channel:<channel_id>:user:<user_id>`
- Pro: User isolation. Con: Parallel conversations clutter channels.

**Recommendation**: Implement Option B. On new message in a text channel, create
a thread with `session.ThreadStart()`, store `thread_id → session_id`, and route
all subsequent messages in that thread to the same session.

### Discord Interactions (Slash Commands)

Support `/ask <question>` via Discord's Application Commands API.
Requires registering a global slash command and handling `INTERACTION_CREATE`
gateway events. The bridge would need to respond within 3 seconds with
`InteractionResponseDeferredChannelMessageWithSource` then follow up.

### Rich Embeds for Tool Output

Replace plain-text tool invocation notices with Discord embeds
(`*discordgo.MessageEmbed`) for better formatting. Color-code by event type:
blue for tool invocations, green for results, red for errors.

### Message Splitting

Discord has a 2000-character message limit. Long LLM responses need to be
split into multiple messages. Strategy: split on paragraph boundaries, keeping
code blocks intact. Implement a `splitMessage(text string, limit int) []string`
helper in `internal/discord/`.
