# Adding New Chat Platform Integrations

Implement a new bridge as a standalone binary in `cmd/<platform>-bridge/`. The bridge:

1. Connects to agentd via WebSocket (`WS /sessions/:id/ws`)
2. Listens for messages from the chat platform
3. Forwards messages to agentd as `{ "type": "send_message", "send_message": { "content": "..." } }`
4. Forwards agentd `chunk` / `done` events back to the platform as messages
5. On `perm_request`: surfaces the request to the user (reactions, modal, etc.) and sends `perm_response`

See `cmd/zulip-bridge/bridge.go` for a complete working example. The Slack, Discord, and Matrix bridges (`cmd/slack-bridge`, `cmd/discord-bridge`, `cmd/matrix-bridge`) follow the same pattern via `internal/bridges/base.go`.

## Shared bridge infrastructure

`internal/bridges/base.go` provides:
- agentd connection setup from `AGENT_ADDRESS` / `AGENTD_API_KEY` env vars
- Session creation and reconnect logic
- Base event loop skeleton

`internal/channels/` defines the `Channel` interface and adapter types that the bridge binaries wire to `internal/bridges/`.

## Protocol reference

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full agentproto WebSocket protocol, event types, and ClientMessage format.
