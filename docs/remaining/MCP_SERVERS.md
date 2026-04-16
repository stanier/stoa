# MCP Servers — Not Yet Started

13 servers are implemented. These 7 are not. See `docs/MCP_SERVERS_ROADMAP.md` for architecture notes and the Claude Code tool parity analysis.

---

## Tier 1 — Capability Unlocks

### `mcp-git` — Git Operations

**Tools**: `git_status`, `git_diff`, `git_log`, `git_add`, `git_commit`, `git_push`, `git_branch`, `git_checkout`, `git_show`

**Config**: `repo_path`, `allow_push` (default false), `allow_force`

**Design notes**:
- Wrap `git` CLI via `os/exec` — no external Go deps
- Read ops (status, log, diff) always safe; write ops (commit, push) gated by config flag
- Hook into `permission_adapter.go` for tool-level confirmation on destructive ops

---

## Tier 2 — High Value Extensions

### `mcp-docs` — Documentation Search

**Tools**: `docs_search`, `docs_lookup`

**Sources**: DevDocs.io API (200+ technologies), MDN, pkg.go.dev, Python docs, Rust std

**Config**: `sources` (list of enabled doc sets), `cache_ttl`

**Design notes**: DevDocs.io public API is the right starting point; cache locally to avoid hammering it.

---

### `mcp-sqlite` — Structured Data Queries

**Tools**: `sql_query`, `sql_execute`, `sql_list_tables`, `sql_schema`, `sql_import_csv`

**Config**: `database_path`, `read_only` (default true)

**Design notes**:
- `read_only = true` by default; only `SELECT` allowed (enforced by statement prefix check)
- Use `modernc.org/sqlite` (pure Go, no CGo)

---

## Tier 3 — Specialized

### `mcp-package-registry` — Package Lookups

**Tools**: `pkg_search`, `pkg_info`, `pkg_versions`, `pkg_readme`

**Registries**: npm, PyPI, crates.io, pkg.go.dev

**Config**: `enabled_registries`, `cache_ttl`

---

### `mcp-vector-db` — Semantic Search / RAG

**Tools**: `embed`, `index_document`, `search_similar`, `list_collections`, `delete_collection`

**Backend options**: sqlite-vec (recommended — zero deps, pure Go), Qdrant, Chroma

**Config**: `backend`, `embedding_model`, `collection_name`

**Design notes**: sqlite-vec is the right default. Defer until there's a concrete RAG use case.

---

## Tier 4 — Infrastructure

### `mcp-docker` — Container Management

**Tools**: `list_containers`, `container_logs`, `container_exec`, `start_container`, `stop_container`, `list_images`

**Implementation**: Docker CLI via `os/exec` or Docker Go SDK

---

## Third-Party Integrations

### `mcp-gitlab` — GitLab API

Design doc in `docs/MCP_INTEGRATIONS_PLAN.md`. Projects, issues, MRs, pipelines.
