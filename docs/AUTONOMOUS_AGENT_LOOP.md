# Autonomous Agent Development Loop

## Concept

An agent-driven feature implementation loop: a task agent is given a spec, spun up in an isolated environment, implements features, validates them, and opens a PR for human review. Nothing reaches production without a human merge.

Inspired by the DGM/hyperagent paper (arXiv:2603.19461), but scoped to **task execution only** — no recursive self-modification of the agent scaffolding.

---

## Proposed Flow

1. Human writes a structured task spec (features, constraints, acceptance criteria)
2. Agent is spawned with a git worktree (`agent/{run-id}` branch) and scoped tools
3. Agent implements, builds (`make build`), runs tests
4. Agent deploys to an isolated compose stack (`agent-run-{id}`)
5. Agent runs integration checks (HTTP, optionally headless browser)
6. Agent pushes branch and opens a PR with a summary
7. Human reviews and merges — this is the only path to any shared environment

---

## Risks and Mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Semantically wrong but compiling changes | High | All changes gated by PR review |
| Secret/credential leakage | High | Isolated env with throwaway creds only; no prod keys |
| Runaway compute (loops, retries) | Medium | Hard step/token budget in agentd; kill switch |
| Push to wrong branch or force-push | Medium | Git server-side restriction: agent creds can only push `refs/heads/agent/*` |
| Test environment state bleeding between runs | Medium | Fresh named compose project per run, torn down after |
| Agent bypasses safety checks (`--no-verify`, etc.) | Medium | Hooks enforced server-side, not just client |
| Prompt injection via codebase content | Medium | Instructions sourced from trusted spec only |

**Core principle**: the agent has no write access to anything that isn't isolated and reversible. Every action is auditable in the PR diff and JSONL trace.

---

## What Needs to Be Built

Primitives that already exist:
- Worktree isolation (`EnterWorktree` / `ExitWorktree`)
- Shell tool (scoped execution)
- JSONL trace logs
- agentd step/compaction limits
- GitHub MCP (PR creation — needs GitLab equivalent via `glab` CLI or GitLab MCP)

Gaps to fill:
1. **Agent task format** — structured job spec: feature list, constraints, acceptance criteria, environment config
2. **Run orchestrator** — sequences: provision worktree → run agent → validate → submit PR → deprovision
3. **Compose-aware tool** — spin up / tear down a named `agent-run-{id}` stack, scoped so it can't touch prod/staging
4. **Headless validation tool** — HTTP checks against test env, optionally a headless browser driver
5. **GitLab integration** — push branch + open MR (via `glab` CLI in shell tool or a dedicated MCP server)

---

## Deferred Decisions

- How tightly the orchestrator couples to agentd vs. being a separate process
- Whether the agent task format lives as a file in the repo, a DB record, or an ACP run payload
- Acceptance criteria format (shell commands to run? endpoint assertions? LLM-graded?)
- Whether to pursue the recursive self-improvement angle from the paper (current recommendation: no — the value of autonomous feature implementation is high without that complexity, and self-modification of scaffolding opens a much harder verification problem)
