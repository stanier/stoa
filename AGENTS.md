# AGENTS.md

This file provides project context for AI coding assistants working in this repository.

See `CONTRIBUTING.md` for the full developer guide: build commands, provider configuration, architecture overview, codebase layout, and common pitfalls.

---

## Quick Reference

**Bubbletea Message Types** (`internal/ui/messages.go`):
- `streamStartMsg` - Triggers streaming state (standalone mode)
- `streamChunkMsg` - Carries content/thinking/toolUse (standalone mode)
- `streamDoneMsg` - Ends streaming (standalone mode)
- `agentEventMsg` - Carries agentproto.Event from agentd (remote mode)
- `agentConnectedMsg` / `agentDisconnectedMsg` - Connection lifecycle (remote mode)
- `compactionNeededMsg` / `compactionDoneMsg` - Compaction flow
- `commandExecutedMsg` - Command result
- `toolResultMsg` - Tool execution result

**agentproto Event Types** (`internal/agentproto/events.go`):
- `EventTypeChunk` - Streaming content/thinking delta
- `EventTypeDone` - End of turn with authoritative full content
- `EventTypeToolInvoked` / `EventTypeToolResult` - Tool observability
- `EventTypePermRequest` - Client must respond via `perm_response` ClientMessage
- `EventTypePermMention` - FYI: tool ran silently
- `EventTypeCompaction` - History was compacted
- `EventTypeError` - Error; `Fatal: true` means session is over

**agentd WebSocket protocol**:
- `POST /sessions` → `{ "session_id": "..." }`
- `GET /sessions` → `[]SessionInfo`
- `WS /sessions/:id/ws` → bidirectional JSON frames
  - Client sends: `{ "type": "send_message", "send_message": { "content": "..." } }`
  - Client sends: `{ "type": "perm_response", "perm_response": { "request_id": "...", "approved": true } }`

**agentproto `SessionConfig.Source`**: Set to `"web"`, `"tui"`, `"zulip"`, `"slack"`, `"discord"`, `"matrix"`, or `"cli"` when creating sessions. Stored on the session and returned in `SessionInfo`. webd filters the sidebar to `source == "web"`.

**authd HTTP API**:
- `POST /oauth/login` → `{ "login_id", "url", "expires_in" }` — starts browser login flow
  - Body may include `"post_login_redirect": "https://..."` (origin must be in `AUTHD_ALLOWED_REDIRECT_ORIGINS`)
- `GET /oauth/login/poll?id=<login_id>` → `202 {status:pending}` or `200 {session_token}`
- `POST /token/verify` (Bearer) → `{ user_id, email, name, expires_at }`
- `POST /token/service` (admin Bearer) → `{ session_token }` — service account token
- `GET /healthz` → `{"status":"ok"}`

**webd HTTP API**:
- `GET /auth/login` — starts OIDC browser flow
- `GET /auth/callback?token=` — receives token from authd, sets HttpOnly cookie
- `POST /auth/logout` — clears cookie
- `GET /api/me` — returns user info from authd (validates cookie)
- `GET|POST /api/sessions[/...]` — proxies to agentd with injected Bearer token
- `WS /ws/:sessionId` — proxies to agentd WebSocket with injected Bearer token

**apikeyd HTTP API**:

Auth flow (OIDC via authd, sets session cookie):
- `GET /auth/login` — starts OIDC browser flow
- `GET /auth/callback` — receives token from authd, sets HttpOnly cookie
- `POST /auth/logout` — clears cookie

Authenticated key management (session cookie required):
- `GET /api/keys` → `[]{ id, name, prefix, created_at, last_used_at?, expires_at?, revoked_at? }`
- `POST /api/keys` body: `{ "name": "...", "expires_in_days": 90 }` → `{ key, id, name, prefix, created_at, expires_at? }` — raw key returned exactly once
- `DELETE /api/keys/{id}` → 204

Key verification (called by inference-gateway; no session — key IS the credential):
- `POST /keys/verify` (`Authorization: Bearer <key>` or `x-api-key: <key>`) → `{ key_id, user_id, name, expires_at }` — `apikeydclient` caches successes for 30s; errors (revoked/expired) are never cached

Admin (requires `APIKEYD_ADMIN_TOKEN`):
- `POST /admin/users/{user_id}/key` → `{ key, id, user_id, prefix, created_at }` — atomically rotates the service key for a user; raw key returned exactly once

Key format: `stoa-infgw_*`

- `GET /healthz` → `{"status":"ok"}`

**auth-admin usage**:
```bash
auth-admin authd migrate [up|down|status|version]
auth-admin authd user list
auth-admin authd user revoke <email>
auth-admin authd service create <id> [--ttl-hours N]

auth-admin idp migrate [up|down|status]
auth-admin idp user list
auth-admin idp user create --username U --email E --password P
auth-admin idp user reset-password --username U --password P
auth-admin idp user unlock --username U
auth-admin idp user delete --username U
```

**Running auth-admin via compose** (recommended — correct DB URL and network already wired):

The migrate services (`migrate-agentd`, `migrate-authd`, `migrate-localidp`, `migrate-apikeyd`) use `profiles: [migrate]` so they are excluded from `podman-compose up` but remain explicitly runnable. Note: podman-compose 1.3.x requires `--profile migrate` even for `run --rm`.

```bash
podman-compose --profile migrate run --rm migrate-agentd

podman-compose --profile migrate run --rm migrate-localidp idp user create \
  --username yourname --email you@example.com --password yourpassword

podman-compose --profile migrate run --rm migrate-authd authd service create zulip-bridge
```

**Critical Configuration Logic** (`internal/config/config.go:Load()`):
1. Provider selection and model are **YAML-only** — defined under `llm.providers` (a list). `LLM_PROVIDER`, `ANTHROPIC_MODEL`, and `OPENAI_MODEL` are **not** read from env vars and are silently ignored.
2. API key env var defaults: `ANTHROPIC_API_KEY` for `type: anthropic`, `OPENAI_API_KEY` otherwise. Override with `api_key_env` in YAML. The env var always wins over a literal `api_key` in YAML.
3. API URL env var defaults: `ANTHROPIC_API_URL` / `OPENAI_API_URL` (or a custom `api_url_env`). If not set, provider defaults are used.
4. If `agent.address` is set and the default provider has no API key, LLM validation is skipped — the client connects to agentd instead of calling an LLM directly (remote mode).
5. agentd requires either `AGENTD_AUTHD_URL` (production auth) or `AGENTD_NO_AUTH=true` (local dev only) to start; missing both is an error.

**Agent Registry** (`/agents/` and `/acp/`):
- `GET /agents` / `POST /agents` — list or create agents
- `GET /agents/{name}` / `PUT /agents/{name}` / `DELETE /agents/{name}` — manage agents
- `POST /agents/{name}/publish` / `POST /agents/{name}/unpublish` — control ACP visibility
- `GET /acp/ping` — health (no auth)
- `GET /acp/agents` — list published agents
- `POST /acp/runs` — create a run (`mode`: `sync`/`async`/`stream`)
- `GET /acp/runs/{id}` — poll status; `POST /acp/runs/{id}` — resume (perm response)
- `POST /acp/runs/{id}/cancel` — cancel run

```bash
auth-admin agentd agent list
auth-admin agentd agent create --name my-agent --display-name "My Agent" --visibility external
auth-admin agentd agent publish --name my-agent
auth-admin agentd agent unpublish --name my-agent
auth-admin agentd agent delete --name my-agent
```

**Message Filtering in `streamFromLLM()`**:
- Filter out system messages (not sent to LLM)
- Filter out summary messages (display only)
- Filter out the empty streaming placeholder message
- Validate sequence ends with user message
