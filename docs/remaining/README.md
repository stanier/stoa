# Remaining Work

**Recently completed (no longer tracked here):**
- Agent registry (CRUD, publish/unpublish, persona seeding) — `internal/agentdb/`, `internal/agentd/handlers_agents.go`
- ACP surface (`/acp/`) — REST+SSE API for running published agents — `internal/agentd/handlers_acp.go`
- SSE keepalive (15 s ticker) and in-memory run cleanup goroutine (24 h TTL)

All outstanding work. Separate trackers for larger areas:

- [Security hardening](SECURITY.md)
- [MCP servers not yet started](MCP_SERVERS.md)
- [Proxy compliance gaps](PROXY_COMPLIANCE.md)

---

## Decision-blocked

### Permission Ranks 3-5 (Authenticated / Authorized / Approved)

**Where**: `internal/permissions/checker.go:74-76`

Ranks 0-2 (Silent/Mention/Request) are fully working. Ranks 3-5 return `"not implemented"`. The auth identity layer they would rely on is now in place (`authd`, `local-idp`, `authdb`).

**What needs building:**
- **Rank 3 — Authenticated**: user must be authenticated before approval is granted
- **Rank 4 — Authorized**: user must have a delegated permission grant (role-based)
- **Rank 5 — Approved**: requires a separate approval workflow (e.g. manager sign-off)

**Implementation options:**

| Option | Effort | Notes |
|--------|--------|-------|
| A: wire to existing authdb session | ~15-20 hrs | Rank 3 checks `user_id` in request context; rank 4 checks `roles` table |
| B: LDAP + external OAuth2 | ~75-105 hrs | Full enterprise SSO; only needed if there's a concrete requirement |

> **Decision needed**: Option A or B? Is there a concrete use case now?

---

### Sub-Agents: Local Mode Bubbletea Polling

**Where**: `internal/agents/agent.go`, `internal/ui/`

Remote agentd mode is fully working. In standalone mode, local `BaseAgent.ReceiveMessages()` channel output is not polled or forwarded as Bubbletea messages, so the TUI side panel stays empty.

**What's needed (~1-2 days):**

Wire a goroutine or `tick`-based poller that reads from `BaseAgent.ReceiveMessages()` and dispatches `agentMessageMsg` / `agentCompletedMsg` into the Bubbletea program.

> **Decision needed**: Dedicated goroutine per agent (cleaner, more responsive) or periodic tick poll (simpler, avoids goroutine proliferation)?
