# Code Redundancy Report

Identified areas of code duplication, ranked by impact.

---

## Critical

### Database Migration Boilerplate
**Files:**
- `internal/authdb/db.go`
- `internal/agentdb/db.go`
- `internal/localidp/db.go`

`Migrate()`, `MigrateDown()`, `MigrateStatus()`, and `openSQLDB()` are >95% identical across all three packages — same goose setup, BaseFS binding, dialect setting, and error wrapping. Only the embedded migration FS and table names differ.

**Recommendation:** Extract shared logic to `internal/db/migrations.go`, accepting the embedded FS and dialect as parameters.

---

## High

### Bridge Session Persistence
**Files:**
- `cmd/slack-bridge/persist.go`
- `cmd/discord-bridge/persist.go`
- `cmd/matrix-bridge/persist.go`

`loadSessionStore()` and `saveSessionStore()` are identical across all three bridges. The only difference is the temp file prefix (`slack-sessions-*.json.tmp`, `discord-sessions-*.json.tmp`, `matrix-sessions-*.json.tmp`). The atomic write pattern and error handling are pure copy-paste.

**Recommendation:** Extract to `internal/bridges/persist.go` with the prefix as a parameter.

---

## Medium

### Channel Adapter Boilerplate
**Files:**
- `internal/channels/slack.go`
- `internal/channels/discord.go`
- `internal/channels/matrix.go`

`Start()`, `Stop()`, `IsConnected()`, and `Messages()` are implemented identically in all three adapters. Type assertion error handling in `SendResponse()`, `AddReaction()`, and `SendMessageWithID()` is also repeated verbatim.

**Recommendation:** Define a base struct (e.g. `baseAdapter`) with embedded channel and lifecycle fields, and promote the shared methods via embedding.

### Hook Firing Methods
**File:** `internal/channels/hooks.go` (lines 94–148)

The same 4-line `Fire*()` pattern is repeated for 8 different events:
```go
func (h *Hooks) Fire<Event>(e HookEvent) {
    if h != nil {
        fire(h.On<Event>, e)
    }
}
```

**Recommendation:** Use a generic helper or code generation to collapse the repeated dispatch methods.

---

## Low

### MCP Server `Initialize()`
**Files:** All `internal/mcpserver/*/server.go` implementations (14 servers)

Every MCP server repeats the same ~12-line `Initialize()` method, differing only in `s.name` and `s.version`.

**Recommendation:** Provide a default `Initialize()` implementation in the `mcpserver` framework (e.g. on a `BaseServer` struct) that reads name/version from a common field, eliminating the per-server boilerplate.
