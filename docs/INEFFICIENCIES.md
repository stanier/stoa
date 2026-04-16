# Codebase Inefficiencies

Findings from a February 2026 audit pass. Separated by confidence and status.

---

## Fixed

### String concatenation in streaming hot paths

All four sites used `+=` on strings inside per-chunk loops, causing O(n²) allocations as responses grew.

| File | Old | New |
|------|-----|-----|
| `internal/ui/model.go:49,51` | `currentStreamMsg/currentThinking string` | `[]byte` |
| `internal/ui/handlers.go:52,55` | `+= msg.content/thinking` | `append([]byte, string...)` |
| `internal/ui/handlers_agent.go:63,66` | `+= chunk.Content/Thinking` | `append([]byte, string...)` |
| `cmd/agentd/loop.go:85,92` | `iterContent/iterThinking string +=` | `strings.Builder` |
| `internal/llm/openai.go:315-316` | `string([]byte) + string → []byte(...)` | `append([]byte, string...)` |

---

## Confirmed — Not Yet Fixed

### A: Full re-render on every viewport update


**Files**: `internal/ui/update.go:297-306`, `internal/ui/view.go:75-164`, `internal/ui/handlers.go:66`

`updateViewport()` unconditionally calls `renderMessages()` which iterates all messages, does markdown parsing (`m.markdownRenderer.Render()`), applies lipgloss styling, and rebuilds the `toolResultClickZones` map from scratch — all on every streaming chunk.

At 10–50 chunks/sec during streaming, this means repeated Markdown parsing of the entire conversation history. Bubbletea's internal terminal diffing hides the re-draw cost but not the string-generation CPU cost.

**Rough fix**: add a dirty flag or cache the last rendered string and skip `SetContent` when unchanged. The streaming message is the only part that changes per-chunk, so it could be rendered separately and appended to a cached prefix.

```go
// Minimal version — skip SetContent when content unchanged
func (m *Model) updateViewport() {
    content := m.renderMessages()
    if content == m.lastViewportContent {
        return
    }
    m.viewport.SetContent(content)
    m.lastViewportContent = content
    m.viewport.GotoBottom()
}
```

Note: comparing large strings has its own cost; a hash or dirty bit would be cheaper.

---

### B: Goroutine leak in agent message subscription ✓ Fixed

**Files**: `internal/ui/model.go:250-271`, `internal/ui/update.go:364-371`

`Subscribe()` returns `(chan Message, func())` — the unsubscribe function. The goroutine spun up in `model.go` discards it with `_`:

```go
go func() {
    msgChan, _ := agentManager.Subscribe()  // unsubscribe discarded
    for msg := range msgChan {
        ...
    }
}()
```

The subscription stays registered in `messageBus` indefinitely. `quit()` (update.go:364) closes the tracer but never calls unsubscribe or closes `agentMsgChan`. The forwarding goroutine blocks on the channel after the UI exits.

In normal single-session usage the impact is one leaked goroutine and one leak in `messageBus`. In tests or if the UI is restarted it accumulates.

**Fix**: capture and defer the unsubscribe function, and close `agentMsgChan` in `quit()`.

```go
go func() {
    msgChan, unsubscribe := agentManager.Subscribe()
    defer unsubscribe()
    for msg := range msgChan {
        ...
    }
}()

// In quit():
func (m Model) quit() tea.Cmd {
    // ... existing tracer close ...
    if m.agentMsgChan != nil {
        close(m.agentMsgChan)
    }
    return tea.Quit
}
```

---

---

## MCP Tool Design — Large Result Antipattern

### Context-flooding from introspection-style MCP tools

**Observed**: `mcp_netbox_netbox_introspect` returned 759,612 bytes (~190K tokens) in a single tool result, consuming the entire context window and stalling the LLM.

**Root cause**: MCP tools that dump entire schemas, API catalogues, or data sets in one call treat the LLM context window as unlimited. A single call to an introspect-style endpoint can exceed 512KB and dwarf the entire rest of the conversation history.

**Mitigation in agentd** (`cmd/agentd/toolpager.go`):
- Results > 50KB are stored in a per-session cache (`sess.resultCache`)
- The LLM receives page 1 (≤50KB) plus a truncation notice with the cache ID
- A lazily registered per-session tool `read_tool_result` lets the LLM fetch subsequent pages

**The right fix is in the tool itself.** Agentd-level pagination is a safety net, not a solution. Well-designed MCP tools should:
- Default to returning a *summary* or *index* (e.g., a list of endpoint names), not full schemas
- Accept filter/search parameters so the LLM can request only what it needs
- Expose pagination parameters (`offset`, `limit`) for large collections
- Offer a `detail` or `verbose` flag that defaults to off

Anthropic's own guidance ("Writing Tools for Agents") states: tools should return <10,000 tokens by default; paginated or filtered access is the gold standard. The reference implementation in Claude Code caps tool results at 25,000 tokens.

---

## Lower Priority (Not Yet Verified)

These were flagged by the initial audit but not confirmed against live code:

- **`internal/ui/view.go`**: Multiple `strings.Count(content, "\n")` calls per message per render pass — redundant line counting. Cache line heights at message creation time.
- **`internal/ui/view.go`**: `m.toolResultClickZones = make(map[int]ClickZone)` allocates a new map every render. Use `clear()` (Go 1.21+) or range-delete instead.
- **`internal/ui/view.go`**: Slice appends in `renderAssistantContent()` without capacity hints — causes repeated reallocations during line parsing.
- **`internal/mcp/client.go`**: `float64 → int64` type assertion on every response to normalize JSON IDs — could normalize at send time instead.
- **`internal/ui/handlers.go`**: Non-blocking send to permission response channel silently drops the response if no receiver is ready.
