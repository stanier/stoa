# Roadmap

Outstanding work, known issues, and decision-blocked items. Contributions welcome on any of these.

- [Performance](#performance)
- [Security Hardening](#security-hardening)
- [Gateway Features](#gateway-features)
- [MCP Servers](#mcp-servers)
- [Bridge Features](#bridge-features)
- [API Proxy Compliance](#api-proxy-compliance)
- [Code Quality](#code-quality)
- [Decision-Blocked](#decision-blocked)

---

## Performance

### Batch DB inserts instead of per-row
**File:** `internal/agentdb/messages.go:16-30`

Messages are inserted one-at-a-time in a loop, producing N round-trips to the database per save. Should be replaced with a multi-value `INSERT` to batch all rows in a single query.

### Reuse HTTP client in token counter
**File:** `internal/compaction/api_token_counter.go:100`

A new `http.Client` is created on every call to `CountMessages()`. Should be a package-level singleton with connection pooling.

### Token counting blocks the hot path
**File:** `internal/agentd/loop.go:336-349`

`maybeAutoCompact` makes a synchronous HTTP call to count tokens after every message, stalling the agent loop. Should be made async (goroutine + result channel) or results should be cached and reused within a turn.

### Message history copied on every LLM call
**File:** `internal/agentd/loop.go:84-85`

The full `sess.messages` slice is copied on every LLM iteration. For long conversations this is an increasingly expensive O(n) allocation in the hot path. Consider a read-only view or copy-on-write approach.

### ACP run cleanup holds lock for full loop
**File:** `internal/agentd/server.go:169-188`

The background cleanup goroutine holds `acpRunsMu` for its entire sweep over all runs. Under load this blocks all run access for the duration. Should snapshot the run list under a short lock, then process outside of it.

### Cleanup goroutine has no shutdown
**File:** `internal/agentd/server.go:169-188`

The background cleanup ticker goroutine has no cancellation signal. It leaks after the server stops. Needs a context or done channel wired to the server's shutdown path.

### MCP reconnect goroutine leak
**File:** `internal/mcp/manager.go:152-197`

`scheduleReconnect` spawns a new goroutine on every server failure with no deduplication guard. Repeated failures produce unbounded goroutines. Should use exponential backoff with a single timer per server.

### Double-lock / thundering herd on auth cache
**File:** `cmd/discord-bridge/bridge.go:169-174`

Auth status is checked under lock, released, then an API call is made, then re-locked to write the result. Multiple goroutines racing on a cache miss all make the API call simultaneously. A `singleflight` or in-flight flag would prevent the thundering herd.

### Full re-render on every viewport update
**File:** `internal/ui/view.go` (markdown rendering)

Markdown parsing and styling runs on every chunk update with no dirty flag or render cache. For long responses this is wasted CPU.

### String concatenation in thinking accumulator
**File:** `internal/agentd/loop.go:161-165`

`finalThinking += "\n" + iterThinkingStr` re-allocates the string on every iteration. A single `strings.Builder` maintained across the turn would eliminate repeated allocations.

### Message filter pre-allocates worst-case
**File:** `internal/agentd/handlers.go:468-473`

Allocates a full-length slice when filtering system messages from a list. Minor overhead in a request-handling path. Could allocate conservatively or use `slices.DeleteFunc`.

---

## Security Hardening

What's already in place: rootless Podman, non-root container users, CapDrop ALL, no-new-privileges, ReadonlyRootfs, IPC isolation, swap disabled, PID limits, network split (internal MCP / agent-external), mcp-shell on isolated `mcp-shell-net` with squid egress, permission policy system, SSRF protection, tool result pagination (50KB cap), filesystem/shell path traversal protection.

### Landlock filesystem sandboxing ⚠️ stalled

Previous attempt stalled on `go get github.com/landlock-lsm/go-landlock` timing out.

**Plan** (ready to implement once dep fetches):

1. Add `github.com/landlock-lsm/go-landlock` to `go.mod`
2. Create `internal/mcpserver/sandbox/landlock.go`:
   ```go
   // RestrictToPath restricts this process's filesystem access via Landlock.
   // Uses BestEffort() so it degrades silently on kernels < 5.13.
   func RestrictToPath(allowedRW string, extraROPaths ...string) error
   ```
3. `internal/mcpserver/filesystem/server.go` — call at startup, restrict to `s.rootDir` only
4. `internal/mcpserver/shellserver/server.go` — restrict to `s.workspaceRoot` + broad read-only system paths

Key API:
```go
landlock.V3.BestEffort().RestrictPaths(
    landlock.RWDirs(allowedRW),
    landlock.RODirs("/usr", "/lib", "/etc"),
)
```

`BestEffort()` is required — without it the process fails to start on kernels < 5.13.

### seccomp for mcp-shell

`exec.CommandContext()` in `internal/mcpserver/shellserver/server.go` runs arbitrary commands with no syscall filtering.

**Fix**: explicit seccomp-bpf filter blocking `ptrace`, `mount`, `keyctl`, `clone` with `CLONE_NEWUSER`. Library option: `github.com/elastic/go-seccomp-bpf`.

### Bubblewrap transport

Alternative to Docker for deployments without a container daemon. `bwrap --unshare-net --unshare-pid --ro-bind /usr /usr` gives a clean sandbox for the shell server.

Would be `internal/mcp/transport/bubblewrap.go` implementing the same `Transport` interface as `docker.go`.

### Podman secrets for API keys

API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.) are in `environment:` and visible via `podman inspect agentd`.

**Fix**: Podman secrets + `_FILE` env var pattern. Requires a `secrets:` section in `docker-compose.yml` and a small update to `internal/config/config.go` to read `ANTHROPIC_API_KEY_FILE` etc.

### Image signing (Sigstore/cosign)

Enforce cosign image verification for non-`:local` images via `~/.config/containers/policy.json`. Not started.

---

## Gateway Features

### Response caching

In-process `MemoryCache` (default) or Redis (multi-replica). Cache key: SHA-256 of provider + model + canonical JSON of messages + tools. `CachingClient` decorator with TTL-based invalidation and per-session no-cache override.

Config: `gateway.cache.enabled`, `gateway.cache.ttl`, `gateway.cache.max_entries`, `gateway.cache.redis_url`.

Note: The `RedisCache` stub and `Cache` interface are already defined — Redis implementation is just filling in `redis.go`.

### Semantic caching
**Decision needed**

Cache responses based on semantic similarity rather than exact hash match. Requires an embedding model call per request (~50–200ms added latency) and a vector store (pgvector on agentdb, or a dedicated Qdrant/Chroma instance). Similarity threshold must be tuned.

### Guardrails
**Decision needed**

Inspect and optionally block/modify LLM inputs and outputs: PII detection/masking, content moderation, prompt injection detection, allow/deny lists.

Decisions needed:
1. Build in-house (regex + heuristics) vs. external API (AWS Comprehend, OpenAI moderation, Lakera Guard)?
2. On violation: block silently, return error to user, or log + pass through?
3. Apply symmetrically (input + output) or input-only?

### Cost-aware routing
**Decision needed**

Route requests to a cheaper model for simple queries. Routing signal options: rule-based tags from caller, classifier call (~100ms overhead), or heuristics (token count, code presence).

### Semantic routing
**Decision needed**

Route requests to a specialised model based on topic (coding, clinical, etc.). Requires a topic classifier or explicit caller tagging.

### Provider breadth: AWS Bedrock + GCP Vertex AI
**Decision needed**

Native SDK integration for Bedrock (AWS SigV4 auth) and Vertex AI (GCP service account auth) — neither is OpenAI-compatible. Most other providers (Groq, Together, Mistral, Ollama) already work via the existing OpenAI-compat client.

### MCP gateway / agent traffic governance

Inspect, log, and enforce policy on MCP tool call traffic: per-user tool restrictions, audit log every tool call with parameters and result, rate limiting separate from LLM calls, context injection into tool parameters.

---

## MCP Servers

7 servers not yet started.

### Tier 1 — Capability Unlocks

#### `mcp-git` — Git Operations

**Tools**: `git_status`, `git_diff`, `git_log`, `git_add`, `git_commit`, `git_push`, `git_branch`, `git_checkout`, `git_show`

**Config**: `repo_path`, `allow_push` (default false), `allow_force`

**Design notes**: Wrap `git` CLI via `os/exec` — no external Go deps. Read ops always safe; write ops gated by config flag. Hook into `permission_adapter.go` for tool-level confirmation on destructive ops.

### Tier 2 — High Value Extensions

#### `mcp-docs` — Documentation Search

**Tools**: `docs_search`, `docs_lookup`

**Sources**: DevDocs.io API (200+ technologies), MDN, pkg.go.dev, Python docs, Rust std

**Config**: `sources` (list of enabled doc sets), `cache_ttl`

#### `mcp-sqlite` — Structured Data Queries

**Tools**: `sql_query`, `sql_execute`, `sql_list_tables`, `sql_schema`, `sql_import_csv`

**Config**: `database_path`, `read_only` (default true)

**Design notes**: `read_only = true` by default; only `SELECT` allowed (enforced by statement prefix check). Use `modernc.org/sqlite` (pure Go, no CGo).

### Tier 3 — Specialized

#### `mcp-package-registry` — Package Lookups

**Tools**: `pkg_search`, `pkg_info`, `pkg_versions`, `pkg_readme`

**Registries**: npm, PyPI, crates.io, pkg.go.dev

#### `mcp-vector-db` — Semantic Search / RAG

**Tools**: `embed`, `index_document`, `search_similar`, `list_collections`, `delete_collection`

**Backend options**: sqlite-vec (recommended — zero deps, pure Go), Qdrant, Chroma

**Design notes**: Defer until there's a concrete RAG use case.

#### `mcp-gitlab` — GitLab API

Projects, issues, MRs, pipelines. See `docs/MCP_INTEGRATIONS_PLAN.md` for architecture notes.

### Tier 4 — Infrastructure

#### `mcp-docker` — Container Management

**Tools**: `list_containers`, `container_logs`, `container_exec`, `start_container`, `stop_container`, `list_images`

**Implementation**: Docker CLI via `os/exec` or Docker Go SDK

---

## Bridge Features

### Slash commands (Discord + Slack)

**Discord**: Support `/ask <question>` via Application Commands API. Requires registering a global slash command and handling `INTERACTION_CREATE` gateway events. Must respond within 3 seconds with `InteractionResponseDeferredChannelMessageWithSource` then follow up.

**Slack**: Support `/ask <question>`. Requires `commands` OAuth scope, handling `slash_commands` socket mode events, and a registered slash command in the Slack App configuration.

### Multi-workspace support (Slack)

A single bridge instance connects to one workspace. Extend `sessionStore` with a workspace ID prefix, or run multiple bridge instances.

### Matrix E2EE

The MVP does not support encrypted rooms. To support E2EE:
- Use `mautrix-go`'s `crypto` subpackage
- Initialize `OlmMachine` with a `CryptoStore` (SQLite or Postgres)
- Register `crypto.OlmMachine` as a sync handler alongside `DefaultSyncer`

---

## API Proxy Compliance

Open items only. Priority tiers: 🔴 P1 Broken · 🟠 P2 High · 🟡 P3 Medium · 🟢 P4 Low

### OpenAI

| Priority | Field | Notes |
|----------|-------|-------|
| 🟡 P3 | `n` | Multiple completions; always returns 1 (requires fan-out) |
| 🟡 P3 | `stream_options` | Client preference ignored; `include_usage` hardcoded upstream |
| 🟡 P3 | Initial `content: ""` streaming delta | Some older SDK versions expect an empty opener before content |

### Anthropic

| Priority | Field | Notes |
|----------|-------|-------|
| 🟡 P3 | `tools[].type` computer use | `"computer_20241022"` and text editor tool not handled |

### Gemini

| Priority | Field | Notes |
|----------|-------|-------|
| 🟡 P3 | `contents[].parts[].inlineData` | Returns 400; vision proxy not implemented |
| 🟡 P3 | `candidates[].safetyRatings[]` | Types defined and deserialized; not surfaced through streaming layer |
| 🟡 P3 | `candidates[].citationMetadata` | Types defined and deserialized; not surfaced through streaming layer |
| 🟢 P4 | `cachedContent` | Context caching reference not implemented |
| 🟢 P4 | `candidates[].groundingMetadata` | Google Search grounding |
| 🟢 P4 | `promptFeedback` | Safety feedback on prompt |

### Cross-cutting

| Priority | Field | Notes |
|----------|-------|-------|
| 🟡 P3 | Rate-limit response headers | Requires threading upstream HTTP headers through the streaming layer |

---

## Code Quality

### Database migration boilerplate
**Files:** `internal/authdb/db.go`, `internal/agentdb/db.go`, `internal/localidp/db.go`

`Migrate()`, `MigrateDown()`, `MigrateStatus()`, and `openSQLDB()` are >95% identical across all three packages — same goose setup, BaseFS binding, dialect setting, and error wrapping. Only the embedded migration FS and table names differ.

**Fix**: Extract shared logic to `internal/db/migrations.go`, accepting the embedded FS and dialect as parameters.

### Bridge session persistence
**Files:** `cmd/slack-bridge/persist.go`, `cmd/discord-bridge/persist.go`, `cmd/matrix-bridge/persist.go`

`loadSessionStore()` and `saveSessionStore()` are identical across all three bridges. The only difference is the temp file prefix.

**Fix**: Extract to `internal/bridges/persist.go` with the prefix as a parameter.

### Channel adapter boilerplate
**Files:** `internal/channels/slack.go`, `internal/channels/discord.go`, `internal/channels/matrix.go`

`Start()`, `Stop()`, `IsConnected()`, and `Messages()` are implemented identically in all three adapters.

**Fix**: Define a `baseAdapter` struct with embedded channel and lifecycle fields; promote the shared methods via embedding.

### Hook firing methods
**File:** `internal/channels/hooks.go:94-148`

The same 4-line `Fire*()` dispatch pattern is repeated for 8 different events.

**Fix**: Use a generic helper or code generation to collapse the repeated dispatch methods.

### MCP server `Initialize()` boilerplate
**Files:** All `internal/mcpserver/*/server.go` implementations (14 servers)

Every MCP server repeats the same ~12-line `Initialize()` method, differing only in `s.name` and `s.version`.

**Fix**: Provide a default `Initialize()` on a `BaseServer` struct in the mcpserver framework that reads name/version from a common field.

---

## Decision-Blocked

### Permission ranks 3-5 (Authenticated / Authorized / Approved)
**Where**: `internal/permissions/checker.go:74-76`

Ranks 0-2 (Silent/Mention/Request) are fully working. Ranks 3-5 return `"not implemented"`. The auth identity layer they depend on is now in place (`authd`, `local-idp`, `authdb`).

**What needs building:**
- **Rank 3 — Authenticated**: user must be authenticated before approval is granted
- **Rank 4 — Authorized**: user must have a delegated permission grant (role-based)
- **Rank 5 — Approved**: requires a separate approval workflow (e.g. manager sign-off)

| Option | Effort | Notes |
|--------|--------|-------|
| A: wire to existing authdb session | ~15-20 hrs | Rank 3 checks `user_id` in request context; rank 4 checks `roles` table |
| B: LDAP + external OAuth2 | ~75-105 hrs | Full enterprise SSO; only needed with a concrete requirement |

### Sub-agents: local mode Bubbletea polling
**Where**: `internal/agents/agent.go`, `internal/ui/`

Remote agentd mode is fully working. In standalone mode, local `BaseAgent.ReceiveMessages()` channel output is not polled or forwarded as Bubbletea messages, so the TUI side panel stays empty.

**What's needed (~1-2 days)**: Wire a goroutine or `tick`-based poller that reads from `BaseAgent.ReceiveMessages()` and dispatches `agentMessageMsg` / `agentCompletedMsg` into the Bubbletea program.
