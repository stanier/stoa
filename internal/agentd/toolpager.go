package agentd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/skills"
)

const (
	// maxToolResultBytes is the soft limit for tool result content passed
	// directly to the LLM context. Results larger than this are stored in the
	// session result cache and the LLM receives only the first page along with
	// a notice explaining how to retrieve subsequent pages via read_tool_result.
	//
	// 50 KB ≈ 12,500 tokens — large enough for most tool responses, small
	// enough to avoid consuming the entire context window in a single turn.
	maxToolResultBytes = 50 * 1024
)

// ReadToolResultTool lets the LLM retrieve a specific page of a large tool
// result that was truncated by maybePageResult. It is registered as a
// per-session tool the first time a result exceeds maxToolResultBytes.
type ReadToolResultTool struct {
	sess *Session
}

func (t *ReadToolResultTool) Name() string { return "read_tool_result" }

func (t *ReadToolResultTool) Description() string {
	return "Retrieve a specific page of a large tool result that was truncated. " +
		"Use the result ID and page number from the truncation notice. " +
		"Prefer targeted tool calls over reading every page sequentially."
}

func (t *ReadToolResultTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "The result ID from the truncation notice",
			},
			"page": map[string]interface{}{
				"type":        "integer",
				"description": "The 1-indexed page number to retrieve",
			},
		},
		Required: []string{"id", "page"},
	}
}

func (t *ReadToolResultTool) Execute(_ context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var input struct {
		ID   string `json:"id"`
		Page int    `json:"page"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return skills.ToolResult{Success: false, Error: "invalid parameters: " + err.Error()}, nil
	}
	if input.Page < 1 {
		return skills.ToolResult{Success: false, Error: "page must be >= 1"}, nil
	}

	t.sess.mu.Lock()
	content, ok := t.sess.resultCache[input.ID]
	t.sess.mu.Unlock()

	if !ok {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("no cached result with id %q (it may have been cleared by compaction)", input.ID),
		}, nil
	}

	total := len(content)
	totalPages := (total + maxToolResultBytes - 1) / maxToolResultBytes

	start := (input.Page - 1) * maxToolResultBytes
	if start >= total {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("page %d out of range (total pages: %d)", input.Page, totalPages),
		}, nil
	}
	end := start + maxToolResultBytes
	if end > total {
		end = total
	}

	header := fmt.Sprintf("[Page %d of %d | bytes %d–%d of %d]\n\n",
		input.Page, totalPages, start+1, end, total)
	return skills.ToolResult{Success: true, Content: header + content[start:end]}, nil
}

// maybePageResult checks whether content exceeds maxToolResultBytes. If it
// does not, the original content is returned unchanged. If it does, the full
// content is stored in the session result cache, the read_tool_result
// per-session tool is lazily registered (including in llmCtx.Tools so the LLM
// sees it on the next API call), and a truncated first page with a pagination
// notice is returned instead.
func maybePageResult(sess *Session, toolName, content string) string {
	if len(content) <= maxToolResultBytes {
		return content
	}

	id := uuid.New().String()[:8]
	total := len(content)
	totalPages := (total + maxToolResultBytes - 1) / maxToolResultBytes

	sess.mu.Lock()
	sess.resultCache[id] = content
	if _, exists := sess.perSessionTools["read_tool_result"]; !exists {
		rtr := &ReadToolResultTool{sess: sess}
		sess.perSessionTools["read_tool_result"] = rtr
		sess.llmCtx.Tools = append(sess.llmCtx.Tools, llm.Tool{
			Name:        rtr.Name(),
			Description: rtr.Description(),
			InputSchema: rtr.Schema(),
		})
	}
	sess.mu.Unlock()

	page1 := content[:maxToolResultBytes]
	notice := fmt.Sprintf(
		"[RESULT TRUNCATED: tool %q returned %d bytes (%d pages at %d bytes/page). "+
			"Showing page 1 of %d (bytes 1–%d).\n"+
			"To read more: read_tool_result({\"id\": %q, \"page\": N}) for N = 2..%d.\n"+
			"Consider whether targeted queries can avoid reading all pages.]\n\n",
		toolName, total, totalPages, maxToolResultBytes,
		totalPages, maxToolResultBytes,
		id, totalPages,
	)
	return notice + page1
}
