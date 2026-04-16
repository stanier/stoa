# Agent Skills — Comprehensive Spec Summary

> **Source**: [agentskills.io](https://agentskills.io) · [Full Spec](https://agentskills.io/specification) · [Integration Guide](https://agentskills.io/integrate-skills) · [Scripts Guide](https://agentskills.io/skill-creation/using-scripts) · [GitHub](https://github.com/agentskills/agentskills)

---

## What Is It?

Agent Skills is an **open, cross-tool format** originally developed by Anthropic for giving AI agents specialized knowledge and workflows on demand. A skill is just a **folder of files** — at minimum a `SKILL.md`. It's adopted by Claude Code, Cursor, GitHub, VS Code, Gemini CLI, OpenHands, and many others.

The core idea is **progressive disclosure**: agents load only name+description at startup (~50–100 tokens/skill), then pull full instructions only when a task matches.

---

## Directory Structure

```
skill-name/           # Must match the `name` field in frontmatter
├── SKILL.md          # Required: metadata + instructions
├── scripts/          # Optional: executable scripts agents can run
├── references/       # Optional: supplementary docs loaded on demand
└── assets/           # Optional: templates, images, data files
```

---

## SKILL.md Format

Every skill's `SKILL.md` has two parts: **YAML frontmatter** and **Markdown body**.

### Frontmatter

```yaml
---
name: pdf-processing
description: Extract text and tables from PDF files, fill forms, merge documents. Use when the user mentions PDFs, forms, or document extraction.
license: Apache-2.0
compatibility: Requires Python 3.10+, poppler-utils
allowed-tools: Bash(python3:*) Read
metadata:
  author: example-org
  version: "1.0"
---
```

#### Fields

| Field | Required | Notes |
|---|---|---|
| `name` | **Yes** | 1–64 chars, `[a-z0-9-]` only, no leading/trailing/consecutive hyphens. Must match parent directory name. |
| `description` | **Yes** | 1–1024 chars. Describe **what** it does and **when** to use it. Include keywords. |
| `license` | No | SPDX identifier or reference to bundled license file. |
| `compatibility` | No | 1–500 chars. Env requirements: OS packages, network, target platform. Omit if no special requirements. |
| `allowed-tools` | No | Space-delimited list of pre-approved tools. Experimental — support varies by agent. |
| `metadata` | No | Arbitrary `string → string` map for custom properties. |

#### `name` rules

```yaml
# Valid
name: pdf-processing
name: data-analysis
name: code-review

# Invalid
name: PDF-Processing      # uppercase not allowed
name: -pdf                # no leading hyphen
name: pdf--processing     # no consecutive hyphens
```

#### `description` guidance

```yaml
# Good — says what AND when
description: Extracts text and tables from PDF files, fills PDF forms, and merges multiple PDFs. Use when working with PDF documents or when the user mentions PDFs, forms, or document extraction.

# Bad — too vague
description: Helps with PDFs.
```

### Body Content

The Markdown body has **no format restrictions**. Write whatever helps agents perform the task. Recommended sections:

- Step-by-step instructions
- Examples of inputs and outputs
- Common edge cases

Keep it **under 500 lines / ~5000 tokens**. Move detailed reference material to `references/`.

---

## Progressive Disclosure Model

| Stage | What loads | Approx. size |
|---|---|---|
| Discovery | `name` + `description` only | ~100 tokens/skill |
| Activation | Full `SKILL.md` body | < 5000 tokens recommended |
| Execution | Files from `scripts/`, `references/`, `assets/` | Loaded on demand |

---

## Optional Directories

### `scripts/`

Executable code agents can run. Scripts should be **self-contained**, handle errors gracefully, and never prompt for interactive input (agents can't respond to TTY prompts).

See [Scripts Guide](https://agentskills.io/skill-creation/using-scripts) for full details — summary below.

### `references/`

Extra docs loaded on demand:
- `REFERENCE.md` — detailed technical reference
- Domain-specific files (`finance.md`, `legal.md`, etc.)

Keep files focused — they load individually into context.

### `assets/`

Static resources: templates, images, schemas, lookup tables.

---

## Scripts: Key Details

([Full guide](https://agentskills.io/skill-creation/using-scripts))

### One-off commands (no `scripts/` dir needed)

Reference existing packages directly in `SKILL.md` instructions:

```bash
uvx ruff@0.8.0 check .           # Python, via uv
npx eslint@9 --fix .              # Node.js
go run golang.org/x/tools/...     # Go
```

Pin versions for reproducibility. State prerequisites in `SKILL.md` or the `compatibility` field.

### Self-contained scripts

Bundle scripts in `scripts/` with **inline dependency declarations** — no separate install step:

**Python (PEP 723 + uv):**
```python
# /// script
# dependencies = ["beautifulsoup4"]
# ///
from bs4 import BeautifulSoup
# ...
```
```bash
uv run scripts/extract.py
```

**Deno:**
```typescript
import * as cheerio from "npm:cheerio@1.0.0";
```
```bash
deno run scripts/extract.ts
```

**Bun:**
```typescript
import * as cheerio from "cheerio@1.0.0";
```
```bash
bun run scripts/extract.ts
```

### Designing scripts for agentic use

| Principle | Why |
|---|---|
| No interactive prompts | Agents can't respond to TTY input — hangs indefinitely |
| `--help` flag | Primary way an agent learns your script's interface |
| Helpful error messages | Directly shapes the agent's next attempt |
| Structured output (JSON/CSV) | Composable in pipelines; parseable by tools like `jq` |
| stdout for data, stderr for diagnostics | Keeps data stream clean |
| Idempotency | Agents may retry |
| `--dry-run` for destructive ops | Let agent preview before committing |
| Meaningful exit codes | Document them so agent understands failure types |
| Predictable output size | Many harnesses truncate at 10–30K chars; default to summaries + pagination flags |

---

## File References

Reference other skill files using **relative paths from skill root**:

```markdown
See [the reference guide](references/REFERENCE.md) for details.

Run: `bash scripts/validate.sh "$INPUT_FILE"`
```

Keep references one level deep. Avoid deep nesting chains.

---

## Integration: How Agents Implement Support

([Full guide](https://agentskills.io/integrate-skills))

### Two approaches

- **Filesystem-based agents** (e.g. Claude Code): model issues shell commands like `cat /path/to/skill/SKILL.md` directly. The model has a general Bash/shell tool and can run arbitrary commands.
- **Tool-based agents**: implement custom tools to trigger skill loading/execution. The agent exposes constrained tools (`load_skill`, `execute_script`, etc.) rather than a raw shell.

### Spec compliance and parity between approaches

Both approaches are **fully spec-compliant** — the spec defines them as equal integration paths. For most capabilities they are functionally equivalent:

| Capability | Filesystem-based | Tool-based | Parity? |
|---|---|---|---|
| Discover & load `SKILL.md` | `cat skill/SKILL.md` | `load_skill` tool | ✅ Full |
| Load `references/` files | `cat references/foo.md` | `load_skill_reference` tool | ✅ Full |
| Load `assets/` files | `cat assets/foo.json` | `load_skill_asset` tool | ✅ Full |
| Execute scripts in `scripts/` | `bash scripts/foo.sh` | `execute_script` tool (wraps `os/exec`) | ✅ Full |
| One-off commands in SKILL.md body | Direct shell execution | ❌ No general shell available | ⚠️ Gap |

**The one meaningful gap**: the spec's scripts guide describes a "one-off commands" pattern where skill authors reference package runners directly in the SKILL.md body text (e.g. `uvx ruff@0.8.0 check .`, `npx eslint@9 --fix .`). This pattern assumes the model has a general shell tool. A tool-based agent with only a constrained `execute_script` tool cannot run these — the model would see the instruction but have no way to act on it.

**Mitigation options**:

- **Add a general `bash_exec` tool** — closes the gap entirely but significantly expands the security surface (essentially becomes filesystem-based).
- **Accept the constraint and document it** — skills targeting this agent should put everything in `scripts/` instead of using one-off commands. The spec itself notes that "when a command grows complex, a tested script in `scripts/` is more reliable." For a headless daemon like `agentd`, this is the recommended path: document that agentd follows the tool-based profile, and skill authors targeting it should use `scripts/` exclusively.

### Implementation steps

1. **Discover** — scan configured directories for folders containing a `SKILL.md`
2. **Load metadata** — parse only frontmatter at startup (keep context low)
3. **Inject into system prompt** — tell the model what skills are available:

```xml
<available_skills>
  <skill>
    <name>pdf-processing</name>
    <description>Extracts text and tables from PDFs...</description>
    <location>/path/to/skills/pdf-processing/SKILL.md</location>
  </skill>
</available_skills>
```

4. **Match** — model reads description and decides when to activate
5. **Activate** — load full `SKILL.md` body into context
6. **Execute** — run scripts, load referenced files as needed

### Security considerations

- **Sandbox** script execution in isolated environments
- **Allowlist** scripts from trusted skills only
- **Confirm** before running potentially dangerous operations
- **Log** all script executions for auditing

---

## Validation

Use the [`skills-ref`](https://github.com/agentskills/agentskills/tree/main/skills-ref) reference library:

```bash
skills-ref validate ./my-skill          # Check frontmatter validity
skills-ref to-prompt ./my-skill ...     # Generate <available_skills> XML
```

---

## Claude Code Specifics

Beyond the base spec, Claude Code adds its own frontmatter fields:

| Field | Effect |
|---|---|
| `user-invocable: true` | Exposes as `/skill-name` slash command |
| `disable-model-invocation: true` | Only user can invoke (not Claude automatically) |
| `context: fork` | Runs in isolated subagent |
| `argument-hint: [filename]` | Autocomplete hint in UI |

Skills can be scoped:
- `~/.claude/skills/<name>/` — personal, all projects
- `.claude/skills/<name>/` — project-scoped
- Enterprise managed settings — org-wide

Arguments passed via `$ARGUMENTS`, `$0`, `$1`, etc. Shell output injected with `` !`command` ``.

---

## Key Links

| Resource | URL |
|---|---|
| Homepage | [agentskills.io](https://agentskills.io) |
| Full Specification | [agentskills.io/specification](https://agentskills.io/specification) |
| What Are Skills? | [agentskills.io/what-are-skills](https://agentskills.io/what-are-skills) |
| Integration Guide | [agentskills.io/integrate-skills](https://agentskills.io/integrate-skills) |
| Scripts Guide | [agentskills.io/skill-creation/using-scripts](https://agentskills.io/skill-creation/using-scripts) |
| Example Skills (GitHub) | [github.com/anthropics/skills](https://github.com/anthropics/skills) |
| Reference Library | [agentskills/agentskills — skills-ref](https://github.com/agentskills/agentskills/tree/main/skills-ref) |
| Claude Code Best Practices | [platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices) |
