# Slack Bridge

The `slack-bridge` binary connects a Slack workspace to an `agentd` server,
following the same pattern as `zulip-bridge`.

## Quick Start

```bash
# Generate a service token for the bridge (requires running authd)
auth-admin authd service create slack-bridge

# Run the bridge
AGENT_ADDRESS=http://localhost:7777 \
  AGENTD_API_KEY=<service-token> \
  AGENTD_AUTHD_URL=http://localhost:8888 \
  SLACK_ENABLED=true \
  SLACK_BOT_TOKEN=xoxb-... \
  SLACK_APP_TOKEN=xapp-... \
  ./bin/debug/slack-bridge
```

## Slack App Setup

1. Create a new Slack App at <https://api.slack.com/apps>
2. Under **OAuth & Permissions**, add Bot Token Scopes:
   - `chat:write` — send messages
   - `reactions:write` — add emoji reactions (for permission requests)
   - `users:read` — look up user info
   - `users:read.email` — resolve email addresses (required for authd account linking)
   - `im:history` — read DM history (required for Socket Mode DM events)
3. Install the app to your workspace; copy the **Bot Token** (`xoxb-...`)
4. Under **Socket Mode**, enable Socket Mode and generate an **App-Level Token** (`xapp-...`)
   with the `connections:write` scope
5. Under **Event Subscriptions**, subscribe to bot events:
   - `message.im` — direct messages to the bot
   - `reaction_added` — emoji reactions (for permission approval)

## Environment Variables

| Variable | Description |
|---|---|
| `SLACK_ENABLED` | Set to `true` to enable |
| `SLACK_BOT_TOKEN` | Bot OAuth token (`xoxb-...`) |
| `SLACK_APP_TOKEN` | App-level token for Socket Mode (`xapp-...`) |
| `SLACK_COMMAND_PREFIX` | Command prefix (default: `!`) |
| `SLACK_PERSONA` | Default agentd persona (default: `default`) |
| `SLACK_SESSION_TTL` | Idle session expiry (default: `24h`) |
| `SLACK_SESSION_FILE` | Session persistence path |
| `AGENT_ADDRESS` | agentd server URL (default: `http://localhost:7777`) |
| `AGENTD_API_KEY` | Service token for agentd auth |
| `AGENTD_AUTHD_URL` | authd URL for account linking |

## Commands (DM only)

| Command | Description |
|---|---|
| `!link` | Link your Slack account to the auth system |
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

**Future enhancement**: Replace emoji reactions with Slack Block Kit interactive
buttons (`block_actions` events) for a better UX. This requires handling
`block_actions` in the socket mode event loop and posting messages with
`blocks` payloads instead of plain text.

---

## Future Work

### Thread Handling

The MVP only handles DMs (`channel_type: im`). Channel messages and threaded
conversations are explicitly skipped. Three options for the next iteration:

**Option A — Per-channel session (simplest)**
- All messages in a channel share one agentd session.
- Key: `channel:<channel_id>`
- Pro: Simple. Con: Conversation context bleeds across unrelated users/threads.

**Option B — Per-thread-root session (recommended)**
- Each top-level message creates a new thread; the bot always replies in-thread.
- Subsequent replies within that thread go to the same agentd session.
- Key: `channel:<channel_id>:thread:<thread_ts>` (or `channel:<channel_id>:msg:<ts>` for new threads)
- Pro: Isolated context per conversation. Natural Slack UX.
- Con: Requires tracking thread_ts → session mapping; bots must reply in-thread.

**Option C — Per-message session**
- Every top-level message spawns an independent session.
- No session reuse; no thread tracking.
- Pro: Maximum isolation. Con: No multi-turn conversations.

**Recommendation**: Implement Option B. When a message has no `thread_ts`, the
bridge posts the reply as a new thread (using `MsgOptionTS` on the reply), and
all subsequent messages in that thread route to the same session.

### Slack Block Kit Permissions

Replace the emoji reaction permission UI with interactive Block Kit buttons.
Requires:
- Posting permission-request messages with `blocks` + `fallback_text`
- Handling `block_actions` socket mode events in `client.go`
- Mapping `action_id` → pending permission request

### Multi-Workspace Support

A single bridge instance connects to one workspace. Extend `sessionStore` with
a workspace ID prefix to support multiple workspaces from one binary, or run
multiple bridge instances.

### Slash Commands

Support `/ask <question>` as an alternative invocation method. Requires:
- `commands` OAuth scope
- Handling `slash_commands` socket mode events
- A registered slash command in the Slack App configuration
