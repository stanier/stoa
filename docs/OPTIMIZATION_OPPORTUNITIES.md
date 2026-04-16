# Optimization Opportunities

Identified performance, concurrency, and reliability issues, ranked by impact.

---

## Critical

### Batch DB inserts instead of per-row
**File:** `internal/agentdb/messages.go:16-30`

Messages are inserted one-at-a-time in a loop, producing N round-trips to the database per save. Should be replaced with a multi-value `INSERT` to batch all rows in a single query.

### Reuse HTTP client in token counter
**File:** `internal/compaction/api_token_counter.go:100`

A new `http.Client` is created on every call to `CountMessages()`. Should be a package-level singleton with connection pooling to avoid repeated TCP connection overhead.

### ACP run cleanup holds lock for full loop
**File:** `internal/agentd/server.go:169-188`

The background cleanup goroutine holds `acpRunsMu` for its entire sweep over all runs. Under load this blocks all run access for the duration. Should snapshot the run list under a short lock, then process outside of it.

### Cleanup goroutine has no shutdown
**File:** `internal/agentd/server.go:169-188`

The background cleanup ticker goroutine has no cancellation signal. It leaks after the server stops. Needs a context or done channel wired to the server's shutdown path.

---

## High

### Message history copied on every LLM call
**File:** `internal/agentd/loop.go:84-85`

The full `sess.messages` slice is copied on every LLM iteration. For long conversations this is an increasingly expensive O(n) allocation in the hot path. Consider a read-only view or copy-on-write approach.

### Token counting blocks the hot path
**File:** `internal/agentd/loop.go:336-349`

`maybeAutoCompact` makes a synchronous HTTP call to count tokens after every message. This stalls the agent loop waiting on an external API. Should be made async (goroutine + result channel) or results should be cached and reused within a turn.

### Silently dropped errors
**Files:**
- `internal/webd/proxy.go:120` — `json.Decode` error discarded with `_`
- `internal/authdclient/client.go:94` — `time.Parse` error discarded; a bad timestamp silently causes a zero-value expiry, potentially bypassing expiry checks

Both should log or return the error rather than ignoring it.

### MCP reconnect goroutine leak
**File:** `internal/mcp/manager.go:152-197`

`scheduleReconnect` spawns a new goroutine on every server failure with no deduplication guard. Repeated failures produce unbounded goroutines. Should use exponential backoff with a single timer per server rather than spawning goroutines.

---

## Medium

### Double-lock / thundering herd on auth cache
**File:** `cmd/discord-bridge/bridge.go:169-174`

Auth status is checked under lock, released, then an API call is made, then re-locked to write the result. Multiple goroutines racing on a cache miss all make the API call simultaneously. A singleflight or in-flight flag would prevent the thundering herd.

### String concatenation in thinking accumulator
**File:** `internal/agentd/loop.go:161-165`

`finalThinking += "\n" + iterThinkingStr` re-allocates the string on every iteration. A single `strings.Builder` maintained across the turn would eliminate repeated allocations.

### Message filter pre-allocates worst-case
**File:** `internal/agentd/handlers.go:468-473`

Allocates a full-length slice when filtering system messages from a list. Minor overhead, but called in a request-handling path. Could allocate conservatively or use `slices.DeleteFunc`.
