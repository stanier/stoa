# MCP Servers Roadmap

Generic MCP servers worth building for our agents and assistants, prioritized by capability impact.

## Implemented

- **mcp-filesystem** — Read/write/search files within a sandboxed root directory (`cmd/mcp-filesystem`)
- **mcp-memory** — Persistent key-value store with TTL, tags, and search (`cmd/mcp-memory`)
- **mcp-fetch** — Fetch URLs and extract readable content for research workflows (`cmd/mcp-fetch`)
- **mcp-time** — Current time, date arithmetic, timezone conversion (`cmd/mcp-time`)
- **mcp-diff** — Generate and apply unified diffs/patches (`cmd/mcp-diff`)
- **mcp-web-search** — Web search via Brave Search API; returns ranked results with titles, URLs, snippets (`cmd/mcp-web-search`)
- **mcp-http** — External API client with domain allowlist and private-IP blocking; supports GET/POST/PUT/DELETE/PATCH, custom headers, and request bodies (`cmd/mcp-http`)
- **mcp-shell** — Shell command execution with workspace isolation, timeout, and output cap (`cmd/mcp-shell`); runs on isolated `mcp-shell-net` with squid egress proxy
- **mcp-trello** — Full Trello board/list/card/comment management with board allowlist (`cmd/mcp-trello`)
- **mcp-zulip** — Zulip messaging: send/get/update/delete messages, reactions, streams, users, attachments (`cmd/mcp-zulip`)
- **mcp-netbox** — Netbox DCIM/IPAM via GraphQL: devices, IPs, prefixes, sites, VLANs, interfaces, introspect (`cmd/mcp-netbox`)
- **mcp-promql** — Prometheus/PromQL interface: instant/range queries, metric/label/series discovery, targets, alerts, rules (`cmd/mcp-promql`)
- **mcp-graphql** — Generic GraphQL client with optional mutation and introspection support (`cmd/mcp-graphql`)

---

## Not yet started

See **[docs/remaining/MCP_SERVERS.md](remaining/MCP_SERVERS.md)** for design specs.

| Server | Tier |
|--------|------|
| `mcp-git` | 1 — needs careful safety design (allow_push gating) |
| `mcp-docs` | 2 |
| `mcp-sqlite` | 2 |
| `mcp-package-registry` | 3 |
| `mcp-vector-db` | 3 |
| `mcp-docker` | 4 |
| `mcp-gitlab` | third-party integration |

---

## Architecture Notes

**Keep servers single-purpose**: The filesystem + git + shell combination is already a powerful coding workflow without merging them. Separate processes give crash isolation, independent deployment, and granular permission grants.

**Hook into the permission system**: The existing `permission_adapter.go` is the right place to gate dangerous operations. Destructive tools (git push, shell run, sql execute) should require explicit user confirmation.

**The memory server is the most unique**: The others (shell, git, HTTP) exist in the broader MCP ecosystem and you could configure third-party servers. But a memory server that integrates specifically with this system's conversation traces and agent personas is more valuable built custom.

**MCP vs. Skills**: MCP servers make sense when the capability:
1. Benefits from process isolation (crashes, resource limits)
2. Could be reused by non-Go MCP clients
3. Has state that should persist independently
4. Needs to be deployed on a different machine than the chat client

Simple stateless operations are better as Skills (registered directly in the skills registry).

## Build Targets

```bash
make build-memory   # Build mcp-memory
make build-all      # Build all binaries including mcp-memory
```

---

## Claude Code Tool Parity Analysis

This section maps Claude Code's built-in tools to the planned MCP servers in this project. The goal is not to replicate them exactly — the problems they solve are what matter, and our implementations will differ by design (sandboxing, permission integration, conversation-level concerns, etc.).

### Covered by existing or planned servers

| Claude Code tool | Problem it solves | Project equivalent |
|---|---|---|
| `Read`, `Write`, `Edit` | Read and modify files; make targeted edits without rewriting whole files | `mcp-filesystem` (✅ exists) |
| `Glob` | Discover files in a codebase by name pattern | `mcp-filesystem` (search/list tools) |
| `Grep` | Search file contents by regex across many files | `mcp-filesystem` (search tools) |
| `Bash` | Execute arbitrary shell commands — build tools, tests, git, etc. | `mcp-shell` (✅ exists), `mcp-git` (Tier 1) |
| `WebFetch` | Fetch a URL and extract readable content for research | `mcp-fetch` (✅ exists) |
| `WebFetch` (API variant) | Call external APIs with arbitrary methods and request bodies | `mcp-http` (✅ exists) |

### Not covered — gaps in the project roadmap

These are problems Claude Code solves that the project has no current equivalent for:

| Claude Code tool | Problem it solves | Notes |
|---|---|---|
| `WebSearch` | Query a search engine for current information beyond training data | Distinct from `mcp-fetch` — returns a ranked list of results rather than a single page. Worth adding to the roadmap. |
| `Task` / subagents | Delegate complex subtasks to a parallel or specialized agent | This is a fundamentally different architecture problem — not an MCP server, but an agent-spawning primitive. Relevant to Phase 4 (Sub-Agents). |
| `TaskCreate/Get/Update/List` | Track multi-step work within a session | Claude Code manages this internally as session state; our agents would need a lightweight task-tracking tool or use `mcp-memory` for persistence. |
| `EnterPlanMode / ExitPlanMode` | Gate implementation behind a structured planning and approval step | This is a UX/conversation-flow primitive, not a capability tool. Relevant to how the Zulip server or TUI surfaces agent intent before acting. |
| `AskUserQuestion` | Pause and prompt the user for a structured choice mid-task | Same — a conversation-flow primitive. The Zulip reaction approval system is the current analogue. |
| `NotebookEdit` | Edit cells in a Jupyter notebook specifically | Very specialized. Only worth pursuing if notebook-heavy workflows become a use case. |

### What the project has that Claude Code does not

| MCP server | Problem it solves | Notes |
|---|---|---|
| `mcp-memory` (✅ exists) | Accumulate knowledge across conversations | Claude Code has no cross-session memory — each context starts fresh. This is a genuine capability advantage for our agents. |
| `mcp-sqlite` (Tier 2) | Query structured data with SQL | Claude Code has no equivalent — it would have to read CSVs or dump tables manually via `Bash`. |
| `mcp-time` (✅ exists) | Reliable, queryable current time and date arithmetic | Claude Code receives the date injected into its system prompt; agents using this project get an explicit, queryable tool instead of relying on prompt injection. |
| `mcp-system` (Tier 3) | CPU/memory/disk metrics | Claude Code would use `Bash` to read `/proc` directly. A dedicated server gives a clean interface and avoids `Bash` access for this specific concern. |
| `mcp-docker` (Tier 3) | Container lifecycle management | Claude Code uses `Bash` + docker CLI. A dedicated server enables permission-gated container ops without granting full shell access. |
| `mcp-diff` (Tier 3) | Generate and apply unified diffs/patches | Claude Code uses `Edit` for targeted replacement; `mcp-diff` enables patch-based workflows where a full file context isn't available. |

---

## Claude Code Tool Reference

Tools available to Claude Code (claude-sonnet-4-5 / claude.ai/code), listed by the problem each solves. Included here as a concrete reference when evaluating what capabilities are worth formalizing as MCP servers.

### File Operations

| Tool | Problem solved |
|---|---|
| `Read` | Read file contents (any path, any size with offset/limit) |
| `Write` | Create or overwrite a file |
| `Edit` | Make a precise string replacement within a file without rewriting the whole thing |
| `Glob` | Find files matching a name pattern (e.g., `**/*.go`) across a directory tree |
| `Grep` | Search file contents by regex; return matching lines or file paths |
| `NotebookEdit` | Replace, insert, or delete a cell in a Jupyter notebook |

### Execution

| Tool | Problem solved |
|---|---|
| `Bash` | Run any shell command — git, make, go, docker, etc. — and capture output |

### Web

| Tool | Problem solved |
|---|---|
| `WebFetch` | Fetch a URL, convert HTML to markdown, and answer a prompt about the content |
| `WebSearch` | Run a web search and get ranked results with summaries and links |

### Agent Delegation

| Tool | Problem solved |
|---|---|
| `Task` | Launch a specialized subagent (Bash, Explore, Plan, general-purpose, etc.) in the foreground or background |
| `TaskOutput` | Read output from a running or completed background task |
| `TaskStop` | Terminate a running background task |

### Task Tracking

| Tool | Problem solved |
|---|---|
| `TaskCreate` | Add a task to the session todo list with a subject and description |
| `TaskGet` | Retrieve full details (description, status, dependencies) for a task by ID |
| `TaskUpdate` | Change a task's status, owner, dependencies, or description |
| `TaskList` | List all tasks in the session with status and blocking relationships |

### Conversation Flow

| Tool | Problem solved |
|---|---|
| `EnterPlanMode` | Switch to planning mode; explore the codebase and design an approach before writing any code |
| `ExitPlanMode` | Present the finished plan to the user and wait for approval before implementing |
| `AskUserQuestion` | Pause mid-task and prompt the user for a structured choice (multiple choice, multi-select) |
| `Skill` | Invoke a user-defined slash command or skill by name |
