# Gateway Feature Roadmap

This document outlines a phased plan to add the four highest-value LLM gateway features to
agentd: provider fallbacks, token budgets, response caching, and provider health metrics.

Priority order is intentional — each phase builds infrastructure the next one depends on.

---

## Infrastructure Summary: What's Needed

| Phase | Feature | New daemon? | New DB? | New dependency? |
|---|---|---|---|---|
| 1 | Provider fallbacks | No | No | No |
| 2 | Token budgets | No | Table in existing agentdb | No |
| 3 | Response caching | No | No (in-process map; optional Redis) | Optional: Redis |
| 4 | Provider health metrics | No | No (in-process rolling window) | No |

**Short answer**: no new daemons, no new containers, no new required dependencies for phases
1–3. Phase 3 optionally accepts a Redis URL for a shared cache in multi-instance deployments.
Phase 4 is purely in-process.

---

## Phase 1: Provider Fallbacks

### What it does

When the primary LLM call returns a retryable error (rate limit, 5xx, timeout), automatically
retry the same request against the next provider in a configured priority list, transparently
to the session.

### What exists today

- `ProviderRegistry` already holds multiple named `Client` instances.
- `runAgenticLoop` (`internal/agentd/loop.go`) already selects a client based on
  `overrides.Provider`; if the override isn't found it falls back to `s.llmClient`.
- `StreamChunk.Error` and the existing error return from `StreamMessage` are the hook points.

### What needs to be built

**1a. Fallback chain config** (`internal/config/config.go`)

Add to `LLMConfig` (or a new top-level `GatewayConfig`):

```yaml
gateway:
  fallback_chain: ["default", "anthropic-backup", "groq-fast"]
  retryable_status_codes: [429, 500, 502, 503, 504]
  per_provider_timeout: 30s
```

Env var: `GATEWAY_FALLBACK_CHAIN=default,anthropic-backup,groq-fast`

**1b. `FallbackClient` wrapper** (`internal/llm/fallback.go`)

A new `Client` implementation that wraps a `ProviderRegistry` and a priority-ordered list of
provider names. Its `StreamMessage` and `SendMessage` iterate the chain:

```
for _, name := range chain:
    client = registry.GetClient(name)
    result, err = client.StreamMessage(...)
    if err == nil && no retryable error in stream:
        return result
    if not retryable(err):
        return err immediately
    log warn + try next
return last error
```

Retryability is determined by inspecting `error` for HTTP status codes (via a small
`isRetryable(err error) bool` helper that checks for `*url.Error`, status code wrappers, or
known provider SDK error types).

**1c. Wire into `NewServer`** (`internal/agentd/server.go`)

When `cfg.Gateway.FallbackChain` is non-empty, replace `s.llmClient` with a `FallbackClient`
constructed from the registry and the chain. No other callsites change.

**1d. Streaming fallback edge case**

If the provider starts streaming (sends some chunks) before hitting an error, the fallback
cannot transparently retry — partial content has already been sent to the client. In this case:
- Do not retry; propagate the stream error as `EventTypeError` with `Fatal: false` so the
  client shows it inline.
- Log the partial-stream failure for the health metrics (Phase 4).

### No new infrastructure needed

Everything lives in existing packages. Fallback config is additive to the existing `Config`
struct. No DB writes. No new processes.

---

## Phase 2: Token Budgets

### What it does

Accumulate per-user token usage across sessions and optionally gate requests when a configured
budget is exceeded. Budgets are per-user (keyed on `userID` from authd) and optionally
per-session.

### What exists today

- `Session.lastInputTokens` and `Session.totalOutputTokens` are updated after every LLM turn
  in `runAgenticLoop`.
- `agentdb.DB` already connects to Postgres (`AGENTD_DATABASE_URL`).
- `sess.userID` is populated from the authd token on session creation.

### What needs to be built

**2a. DB migration** (`internal/agentdb/migrations/`)

A new migration (next sequential number) creates:

```sql
CREATE TABLE token_usage (
    id            BIGSERIAL PRIMARY KEY,
    user_id       TEXT        NOT NULL,
    session_id    TEXT        NOT NULL,
    provider      TEXT        NOT NULL,
    model         TEXT,
    input_tokens  INT         NOT NULL DEFAULT 0,
    output_tokens INT         NOT NULL DEFAULT 0,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX token_usage_user_id_idx ON token_usage (user_id, recorded_at DESC);

CREATE TABLE token_budgets (
    user_id           TEXT PRIMARY KEY,
    daily_input_limit   BIGINT,   -- NULL = unlimited
    daily_output_limit  BIGINT,
    monthly_input_limit BIGINT,
    monthly_output_limit BIGINT,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**2b. `TokenLedger`** (`internal/agentdb/token_ledger.go`)

Two methods on `agentdb.DB`:
- `RecordTokens(ctx, userID, sessionID, provider, model string, input, output int) error`
  — INSERT into `token_usage`; called at end of each LLM turn.
- `CheckBudget(ctx, userID string) (ok bool, reason string, err error)`
  — runs a single aggregation query to sum today's/this month's usage and compare to the
  `token_budgets` row for this user. Returns `ok=true` if no budget row exists (default:
  unlimited).

**2c. Wire into `runAgenticLoop`** (`internal/agentd/loop.go`)

Two call sites:

1. **Pre-flight check** at the top of the loop, before `StreamMessage`:
   ```go
   if s.agentDB != nil && sess.userID != "" {
       if ok, reason, err := s.agentDB.CheckBudget(ctx, sess.userID); !ok {
           return ..., fmt.Errorf("budget exceeded: %s", reason)
       }
   }
   ```

2. **Record** after the `Done` chunk is received:
   ```go
   if s.agentDB != nil && sess.userID != "" {
       providerName := resolveProviderName(s, sess)
       _ = s.agentDB.RecordTokens(ctx, sess.userID, sess.id, providerName, model,
           chunk.InputTokens, chunk.OutputTokens)
   }
   ```

**2d. Budget management** (`auth-admin agentd budget` subcommand)

New subcommand in `cmd/auth-admin`:
```
auth-admin agentd budget set --user <email-or-id> --daily-input 100000 --monthly-output 2000000
auth-admin agentd budget get --user <email-or-id>
auth-admin agentd budget reset --user <email-or-id>
```

Optionally expose `GET /api/usage` and `GET /api/budget` via `webd` for the UI.

### Infrastructure impact

- Requires `AGENTD_DATABASE_URL` (already optional today; must be set to enable budgets).
- One new table migration — runs via the existing `auth-admin agentd migrate` path.
- No new daemons, no new services.

---

## Phase 3: Response Caching

### What it does

Cache LLM responses keyed on `(provider, model, messages-hash)`. On a cache hit, stream the
cached content back without calling the LLM. Reduces cost and latency for repeated queries
(sub-agent re-runs, dev/test, identical tool call patterns).

Two tiers:
1. **In-process** (default): `sync.Map` with TTL, zero dependencies.
2. **Redis** (optional): shared across multiple agentd instances; enabled via
   `GATEWAY_CACHE_REDIS_URL`.

### What needs to be built

**3a. Cache key** (`internal/llm/cache/key.go`)

```go
func CacheKey(provider, model string, messages []chat.Message, tools []llm.Tool) string
```

SHA-256 of `provider + model + canonical JSON(messages) + canonical JSON(tools)`.
`json.Marshal` is deterministic for structs (field order is fixed). For `json.RawMessage`
fields, normalise via `json.Compact` before hashing.

**3b. `ResponseCache` interface** (`internal/llm/cache/cache.go`)

```go
type Entry struct {
    Content  string
    Thinking string
    InputTokens  int
    OutputTokens int
    CachedAt time.Time
}

type Cache interface {
    Get(key string) (*Entry, bool)
    Set(key string, entry Entry, ttl time.Duration)
    Delete(key string)
    Stats() Stats
}
```

Two implementations:
- `MemoryCache` — `sync.Map` + background TTL sweeper goroutine.
- `RedisCache` — `github.com/redis/go-redis/v9`; keys prefixed `stoa:llm:`.

**3c. Config**

```yaml
gateway:
  cache:
    enabled: true
    ttl: 1h
    max_entries: 10000      # memory cache only
    redis_url: ""           # if set, use Redis
```

Env: `GATEWAY_CACHE_ENABLED=true`, `GATEWAY_CACHE_TTL=1h`,
`GATEWAY_CACHE_REDIS_URL=redis://localhost:6379`.

**3d. `CachingClient` wrapper** (`internal/llm/cache/caching_client.go`)

A `Client` decorator (wraps any `Client`, including `FallbackClient` from Phase 1):

```go
type CachingClient struct {
    inner llm.Client
    cache Cache
    ttl   time.Duration
}

func (c *CachingClient) StreamMessage(ctx, msgs, tools) (<-chan StreamChunk, error) {
    key := CacheKey(...)
    if entry, ok := c.cache.Get(key); ok {
        // synthesise a channel that emits a single Done chunk with the cached content
        return syntheticStream(entry), nil
    }
    ch, err := c.inner.StreamMessage(ctx, msgs, tools)
    if err != nil { return ch, err }
    return c.recordingStream(key, ch), nil  // tees stream; stores on Done
}
```

**3e. Cache invalidation**

- TTL-based only (simplest; correct for LLMs since prompts don't change in-place).
- Per-session cache-bust: if `sess.overrides.NoCache == true` (new field), skip cache lookup.
- The web UI can expose a "clear cache" button that calls `DELETE /api/cache` (agentd endpoint).

**3f. Wire into `NewServer`**

```go
if cfg.Gateway.Cache.Enabled {
    s.llmClient = cache.NewCachingClient(s.llmClient, cacheImpl, cfg.Gateway.Cache.TTL)
}
```

`CachingClient` wraps whatever `s.llmClient` is at that point — so it composes naturally with
`FallbackClient` from Phase 1.

### Infrastructure impact

- **Default (memory)**: zero new dependencies.
- **Redis**: add `github.com/redis/go-redis/v9` to `go.mod`; run a Redis container. Redis is
  only needed if you want a shared cache across multiple agentd replicas. A single-instance
  deployment gets full benefit from the in-process cache.

---

## Phase 4: Provider Health Metrics

### What it does

Track per-provider rolling statistics (request count, error rate, P95 latency) in memory.
Expose them on a `GET /metrics` endpoint. Used by Phase 1's `FallbackClient` to prefer
healthier providers and skip providers with high recent error rates.

### What needs to be built

**4a. `ProviderStats`** (`internal/llm/stats/stats.go`)

A ring-buffer-based rolling window (last N=1000 samples per provider):

```go
type Sample struct {
    DurationMs int64
    IsError    bool
}

type ProviderStats struct {
    mu      sync.Mutex
    samples []Sample  // ring buffer
    head    int
    count   int
}

func (s *ProviderStats) Record(durationMs int64, isError bool)
func (s *ProviderStats) ErrorRate() float64   // fraction of recent samples that errored
func (s *ProviderStats) P95LatencyMs() int64
func (s *ProviderStats) RequestCount() int
```

**4b. `StatsRegistry`** (`internal/llm/stats/registry.go`)

Map from provider name → `*ProviderStats`. Thread-safe. Singleton on `Server`.

**4c. Instrument `FallbackClient`** (Phase 1)

In `FallbackClient.StreamMessage`, wrap each provider call with timing:

```go
start := time.Now()
ch, err := client.StreamMessage(...)
elapsed := time.Since(start).Milliseconds()
statsReg.Get(name).Record(elapsed, err != nil || streamHadError)
```

**4d. `GET /metrics`** (`internal/agentd/handlers.go`)

Returns JSON (or optionally Prometheus text format):

```json
{
  "providers": {
    "default": { "request_count": 1420, "error_rate": 0.002, "p95_latency_ms": 843 },
    "anthropic-backup": { "request_count": 37, "error_rate": 0.0, "p95_latency_ms": 1200 }
  },
  "cache": { "hits": 312, "misses": 1108, "hit_rate": 0.22 }
}
```

Accept header `application/openmetrics-text` returns Prometheus-compatible output for
Grafana/Prometheus scraping without any additional agent.

**4e. Health-aware routing in `FallbackClient`**

After Phase 4 is instrumented, update `FallbackClient` to prefer providers with lower error
rate: sort the fallback chain by `errorRate ASC, p95Latency ASC` at the start of each
request, rather than using static ordering. The configured chain order becomes a tiebreaker /
priority hint rather than strict ordering.

### Infrastructure impact

Zero. All in-process. The `/metrics` endpoint is additive to `Server.Router()`.

---

## Build Order and Dependencies

```
Phase 1 (fallbacks)          — no dependencies; can start immediately
    └─ Phase 4 (health)      — depends on Phase 1's FallbackClient existing to instrument
Phase 2 (token budgets)      — depends only on agentDB existing (already optional)
Phase 3 (caching)            — depends on nothing; can start immediately in parallel with Phase 1
```

Suggested sequencing:
1. Phase 1 (fallbacks) + Phase 3 (caching) in parallel — both are self-contained.
2. Phase 2 (token budgets) — requires deciding on DB schema.
3. Phase 4 (health metrics) — add instrumentation to Phase 1's FallbackClient; expose endpoint.

---

## File Map

| New/modified file | Purpose |
|---|---|
| `internal/llm/fallback.go` | `FallbackClient` implementation |
| `internal/llm/cache/key.go` | Cache key derivation |
| `internal/llm/cache/cache.go` | `Cache` interface + `MemoryCache` |
| `internal/llm/cache/redis.go` | `RedisCache` (optional) |
| `internal/llm/cache/caching_client.go` | `CachingClient` decorator |
| `internal/llm/stats/stats.go` | `ProviderStats` ring buffer |
| `internal/llm/stats/registry.go` | `StatsRegistry` |
| `internal/agentdb/token_ledger.go` | `RecordTokens` + `CheckBudget` |
| `internal/agentdb/migrations/NNNN_token_budgets.sql` | Token usage + budget tables |
| `internal/config/config.go` | `GatewayConfig` additions |
| `internal/agentd/server.go` | Wire FallbackClient, CachingClient, StatsRegistry |
| `internal/agentd/loop.go` | Pre-flight budget check + token recording |
| `internal/agentd/handlers.go` | `GET /metrics` endpoint |
| `cmd/auth-admin/agentd_budget.go` | Budget management CLI subcommand |

Existing files that require **minimal** modification:
- `internal/agentd/loop.go` — 2 call sites (budget check + token record)
- `internal/agentd/server.go` — client wiring in `NewServer`
- `internal/config/config.go` — new `Gateway` config block

---

## What This Does NOT Require

- **No new daemon or container** for any of the four phases.
- **No Redis** unless you want a shared response cache across multiple agentd replicas.
- **No schema changes to existing tables** — only additive new tables.
- **No changes to `webd`, `authd`, or `local-idp`** — these are purely agentd internals.
- **No changes to the WebSocket protocol** (agentproto) — clients see no difference except
  that responses may arrive faster (cache hit) or from a different provider (fallback).
