# Matrix Bridge

The `matrix-bridge` binary connects a Matrix homeserver to an `agentd` server,
following the same pattern as `zulip-bridge`, `slack-bridge`, and `discord-bridge`.

## Quick Start

```bash
# Generate a service token for the bridge (requires running authd)
auth-admin authd service create matrix-bridge

# Run the bridge
AGENT_ADDRESS=http://localhost:7777 \
  AGENTD_API_KEY=<service-token> \
  AGENTD_AUTHD_URL=http://localhost:8888 \
  MATRIX_ENABLED=true \
  MATRIX_HOMESERVER_URL=https://matrix.org \
  MATRIX_USERNAME=@bot:matrix.org \
  MATRIX_PASSWORD=secret \
  ./bin/debug/matrix-bridge
```

## Matrix Setup

1. Register a bot account on your homeserver (e.g. `@stoa:matrix.org`)
2. Log in and obtain the password (or generate an access token via `/_matrix/client/v3/login`)
3. Invite the bot to the rooms/DMs you want it to handle

**Privileged access not required** — the bridge uses standard Client-Server API features only.

## Environment Variables

| Variable | Description |
|---|---|
| `MATRIX_ENABLED` | Set to `true` to enable |
| `MATRIX_HOMESERVER_URL` | Homeserver URL (e.g. `https://matrix.org`) |
| `MATRIX_USERNAME` | Bot Matrix user ID (e.g. `@bot:matrix.org`) |
| `MATRIX_PASSWORD` | Bot password (used if `MATRIX_ACCESS_TOKEN` not set) |
| `MATRIX_ACCESS_TOKEN` | Bot access token (alternative to password) |
| `MATRIX_COMMAND_PREFIX` | Command prefix (default: `!`) |
| `MATRIX_PERSONA` | Default agentd persona (default: `default`) |
| `MATRIX_SESSION_TTL` | Idle session expiry (default: `24h`) |
| `MATRIX_SESSION_FILE` | Session persistence path |
| `AGENT_ADDRESS` | agentd server URL (default: `http://localhost:7777`) |
| `AGENTD_API_KEY` | Service token for agentd auth |
| `AGENTD_AUTHD_URL` | authd URL for account linking |

## Commands (DM only)

| Command | Description |
|---|---|
| `!link` | Link your Matrix account to the auth system |
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

**Note**: Matrix uses the event ID (string like `$abc123:server.com`) as the
reaction target. The `reactionIndex` is keyed by `roomID:eventID`.

---

## Future Work

### Room/Thread Handling

The MVP only handles DMs. Room messages require joining the room and listening for
`m.room.message` events with the full `IntentGuildMessages` equivalent. Three options:

**Option A — Per-room session (simplest)**
- All messages in a Matrix room share one agentd session.
- Key: `room:<room_id>`
- Pro: Simple. Con: Context bleeds across users.

**Option B — Per-thread session (recommended)**
- Use Matrix threads (`m.thread` relation type) to isolate conversations.
- Each root message starts a thread; bot replies in-thread.
- Key: `thread:<thread_root_event_id>`
- Pro: Isolated context, natural Matrix UX. Con: Requires thread support on client.

**Option C — Per-user-per-room session**
- Each (user, room) pair gets its own session.
- Key: `room:<room_id>:user:<user_id>`
- Pro: User isolation. Con: Parallel conversations clutter rooms.

**Recommendation**: Implement Option B. On new message in a room, create a thread
reply; store `root_event_id → session_id`; route all subsequent thread replies to
the same session.

### Rich Formatted Messages

Matrix supports `m.room.message` with `format: org.matrix.custom.html` and
`formatted_body` for HTML rendering. Use this for tool invocation notices, permission
requests, and code blocks. Replace the current plain-text `sendMessage` with a helper
that sends both `body` (plain fallback) and `formatted_body` (HTML).

### End-to-End Encryption (E2EE)

The MVP does not support E2EE rooms. To support encrypted rooms:
- Use `mautrix-go`'s `crypto` subpackage (`mautrix/crypto`)
- Initialize `OlmMachine` with a `CryptoStore` (SQLite or Postgres)
- Register `crypto.OlmMachine` as a sync handler alongside the DefaultSyncer

### Message Splitting

Matrix has no hard message length limit, but clients may truncate long messages.
Consider splitting responses longer than 32 KB on paragraph boundaries, keeping
code blocks intact.

### Sync Token Persistence

The MVP uses mautrix's in-memory `MemorySyncStore`. For production, persist the
`next_batch` token to disk (or SQLite) so the bridge resumes from where it left off
after restarts and doesn't miss messages. Implement `mautrix.SyncStore` backed by a
file alongside the session store.
