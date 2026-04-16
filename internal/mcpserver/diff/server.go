package diff

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
)

const (
	defaultContextLines = 3
	defaultMaxInputBytes = 512 * 1024 // 512KB per input
)

// Server implements the MCP diff server. It computes line-based unified diffs
// and applies patches. Uses a pure-Go LCS algorithm; no external dependencies.
type Server struct {
	mcpserver.BaseServer
	maxInputBytes int64
	handlers      map[string]mcpserver.ToolHandler
}

// NewServer creates a new diff MCP server.
func NewServer(name, version string, maxInputBytes int64) *Server {
	if maxInputBytes <= 0 {
		maxInputBytes = defaultMaxInputBytes
	}
	s := &Server{
		BaseServer:    mcpserver.BaseServer{Name: name, Version: version},
		maxInputBytes: maxInputBytes,
		handlers:      make(map[string]mcpserver.ToolHandler),
	}
	s.handlers["diff_strings"] = s.handleDiffStrings
	s.handlers["diff_files"] = s.handleDiffFiles
	s.handlers["apply_patch"] = s.handleApplyPatch
	return s
}

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "diff_strings",
			Description: "Compute a unified diff between two text strings. Returns the diff in standard unified diff format.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{
						"type":        "string",
						"description": "Original text",
					},
					"b": map[string]interface{}{
						"type":        "string",
						"description": "Modified text",
					},
					"label_a": map[string]interface{}{
						"type":        "string",
						"description": `Label for the original (shown in the --- header). Defaults to "a".`,
					},
					"label_b": map[string]interface{}{
						"type":        "string",
						"description": `Label for the modified version (shown in the +++ header). Defaults to "b".`,
					},
					"context_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of unchanged context lines around each change. Defaults to 3.",
					},
				},
				"required": []string{"a", "b"},
			},
		},
		{
			Name:        "diff_files",
			Description: "Compute a unified diff between two files on disk.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path_a": map[string]interface{}{
						"type":        "string",
						"description": "Path to the original file",
					},
					"path_b": map[string]interface{}{
						"type":        "string",
						"description": "Path to the modified file",
					},
					"context_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of unchanged context lines around each change. Defaults to 3.",
					},
				},
				"required": []string{"path_a", "path_b"},
			},
		},
		{
			Name:        "apply_patch",
			Description: "Apply a unified diff patch to a text string and return the result. The patch must be in standard unified diff format.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"source": map[string]interface{}{
						"type":        "string",
						"description": "Original text to patch",
					},
					"patch": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff patch to apply",
					},
				},
				"required": []string{"source", "patch"},
			},
		},
	}, nil
}

func (s *Server) Shutdown(_ context.Context) error { return nil }

func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

// --- handlers ---

func (s *Server) handleDiffStrings(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	a := mcpserver.StrArg(args, "a", "")
	b := mcpserver.StrArg(args, "b", "")
	labelA := mcpserver.StrArg(args, "label_a", "a")
	labelB := mcpserver.StrArg(args, "label_b", "b")
	ctx := mcpserver.IntArg(args, "context_lines", defaultContextLines)

	if int64(len(a)) > s.maxInputBytes || int64(len(b)) > s.maxInputBytes {
		return mcpserver.ErrorResult(fmt.Sprintf("input exceeds maximum size of %d bytes", s.maxInputBytes)), nil
	}

	linesA := splitLines(a)
	linesB := splitLines(b)
	result := unifiedDiff(linesA, linesB, labelA, labelB, ctx)
	if result == "" {
		return mcpserver.TextResult("(no differences)"), nil
	}
	return mcpserver.TextResult(result), nil
}

func (s *Server) handleDiffFiles(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	pathA := mcpserver.StrArg(args, "path_a", "")
	pathB := mcpserver.StrArg(args, "path_b", "")
	ctxLines := mcpserver.IntArg(args, "context_lines", defaultContextLines)
	if pathA == "" {
		return mcpserver.ErrorResult("path_a is required"), nil
	}
	if pathB == "" {
		return mcpserver.ErrorResult("path_b is required"), nil
	}

	a, err := readFileLimited(pathA, s.maxInputBytes)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not read path_a %q: %v", pathA, err)), nil
	}
	b, err := readFileLimited(pathB, s.maxInputBytes)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not read path_b %q: %v", pathB, err)), nil
	}

	linesA := splitLines(a)
	linesB := splitLines(b)
	result := unifiedDiff(linesA, linesB, pathA, pathB, ctxLines)
	if result == "" {
		return mcpserver.TextResult("(no differences)"), nil
	}
	return mcpserver.TextResult(result), nil
}

func (s *Server) handleApplyPatch(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	source := mcpserver.StrArg(args, "source", "")
	patch := mcpserver.StrArg(args, "patch", "")
	if patch == "" {
		return mcpserver.ErrorResult("patch is required"), nil
	}

	result, err := applyPatch(source, patch)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("patch failed: %v", err)), nil
	}
	return mcpserver.TextResult(result), nil
}

// --- diff algorithm ---

// diffOp represents a single diff operation.
type diffOp struct {
	kind byte   // ' ' context, '+' add, '-' remove
	text string // line text (without trailing newline)
}

// computeDiff returns the edit operations to transform a into b.
// Uses an LCS-based approach; practical for files up to ~5000 lines.
func computeDiff(a, b []string) []diffOp {
	m, n := len(a), len(b)
	// Build LCS DP table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to build the diff ops (in reverse)
	ops := make([]diffOp, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && a[i-1] == b[j-1] {
			ops = append(ops, diffOp{' ', a[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, diffOp{'+', b[j-1]})
			j--
		} else {
			ops = append(ops, diffOp{'-', a[i-1]})
			i--
		}
	}

	// Reverse
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// unifiedDiff formats a diff as a unified diff string.
func unifiedDiff(a, b []string, labelA, labelB string, ctxLines int) string {
	ops := computeDiff(a, b)

	// Collect hunk ranges: find changed regions and expand by ctxLines.
	type hunkRange struct{ start, end int } // indices into ops
	var hunks []hunkRange

	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		// Found a change; expand hunk
		start := i - ctxLines
		if start < 0 {
			start = 0
		}
		// Scan forward to find end of changes + trailing context
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
			} else {
				// Collect up to ctxLines of trailing context
				tail := 0
				for end < len(ops) && ops[end].kind == ' ' && tail < ctxLines {
					end++
					tail++
				}
				// If more changes follow within ctxLines, keep going
				if end < len(ops) && ops[end].kind != ' ' {
					continue
				}
				break
			}
		}
		hunks = append(hunks, hunkRange{start, end})
		i = end
	}

	if len(hunks) == 0 {
		return ""
	}

	// Merge overlapping hunks
	merged := []hunkRange{hunks[0]}
	for _, h := range hunks[1:] {
		last := &merged[len(merged)-1]
		if h.start <= last.end {
			if h.end > last.end {
				last.end = h.end
			}
		} else {
			merged = append(merged, h)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", labelA)
	fmt.Fprintf(&sb, "+++ %s\n", labelB)

	// Track line numbers in old and new files
	oldLine := 1
	newLine := 1
	opIdx := 0 // current position in ops

	for _, h := range merged {
		// Advance counters to hunk start
		for opIdx < h.start {
			if ops[opIdx].kind != '+' {
				oldLine++
			}
			if ops[opIdx].kind != '-' {
				newLine++
			}
			opIdx++
		}

		// Count lines in hunk for the @@ header
		oldCount, newCount := 0, 0
		for _, op := range ops[h.start:h.end] {
			if op.kind != '+' {
				oldCount++
			}
			if op.kind != '-' {
				newCount++
			}
		}

		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldLine, oldCount, newLine, newCount)

		for opIdx < h.end {
			op := ops[opIdx]
			switch op.kind {
			case ' ':
				fmt.Fprintf(&sb, " %s\n", op.text)
				oldLine++
				newLine++
			case '-':
				fmt.Fprintf(&sb, "-%s\n", op.text)
				oldLine++
			case '+':
				fmt.Fprintf(&sb, "+%s\n", op.text)
				newLine++
			}
			opIdx++
		}
	}

	return sb.String()
}

// --- patch application ---

type hunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []hunkLine
}

type hunkLine struct {
	kind byte   // ' ', '+', '-'
	text string // without prefix char, without trailing newline
}

func parseUnifiedDiff(patch string) ([]hunk, error) {
	lines := strings.Split(patch, "\n")
	var hunks []hunk
	i := 0

	// Skip --- and +++ headers
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "--- ") || strings.HasPrefix(lines[i], "+++ ") {
			i++
		} else {
			break
		}
	}

	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "@@ ") {
			i++
			continue
		}

		var oldStart, oldCount, newStart, newCount int
		_, err := fmt.Sscanf(line, "@@ -%d,%d +%d,%d @@", &oldStart, &oldCount, &newStart, &newCount)
		if err != nil {
			// Try without counts (e.g., @@ -1 +1 @@)
			_, err2 := fmt.Sscanf(line, "@@ -%d +%d @@", &oldStart, &newStart)
			if err2 != nil {
				return nil, fmt.Errorf("could not parse hunk header %q: %v", line, err)
			}
			oldCount = 1
			newCount = 1
		}
		i++

		h := hunk{
			oldStart: oldStart,
			oldCount: oldCount,
			newStart: newStart,
			newCount: newCount,
		}

		for i < len(lines) {
			l := lines[i]
			if strings.HasPrefix(l, "@@ ") {
				break
			}
			if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") {
				break
			}
			if len(l) == 0 {
				// Empty line within hunk — treat as context (trailing newline from split)
				h.lines = append(h.lines, hunkLine{' ', ""})
				i++
				continue
			}
			switch l[0] {
			case ' ':
				h.lines = append(h.lines, hunkLine{' ', l[1:]})
			case '+':
				h.lines = append(h.lines, hunkLine{'+', l[1:]})
			case '-':
				h.lines = append(h.lines, hunkLine{'-', l[1:]})
			case '\\':
				// "\ No newline at end of file" — skip
			default:
				// Treat unrecognized lines as end of hunk
				break
			}
			i++
		}

		hunks = append(hunks, h)
	}

	return hunks, nil
}

func applyPatch(source, patch string) (string, error) {
	hunks, err := parseUnifiedDiff(patch)
	if err != nil {
		return "", fmt.Errorf("invalid patch: %v", err)
	}
	if len(hunks) == 0 {
		return source, nil
	}

	srcLines := splitLines(source)
	out := make([]string, 0, len(srcLines))
	srcIdx := 0 // 1-based current position in srcLines (we'll convert to 0-based)

	for hi, h := range hunks {
		// Copy source lines before this hunk
		hunkStart := h.oldStart - 1 // convert to 0-based
		if hunkStart < 0 {
			hunkStart = 0
		}
		if hunkStart > len(srcLines) {
			return "", fmt.Errorf("hunk %d: oldStart %d is past end of source (%d lines)", hi+1, h.oldStart, len(srcLines))
		}
		for srcIdx < hunkStart {
			out = append(out, srcLines[srcIdx])
			srcIdx++
		}

		// Apply hunk lines
		for _, hl := range h.lines {
			switch hl.kind {
			case ' ':
				// Context: must match source
				if srcIdx >= len(srcLines) {
					return "", fmt.Errorf("hunk %d: context line %q past end of source", hi+1, hl.text)
				}
				if srcLines[srcIdx] != hl.text {
					return "", fmt.Errorf("hunk %d: context mismatch at line %d: expected %q, got %q",
						hi+1, srcIdx+1, hl.text, srcLines[srcIdx])
				}
				out = append(out, srcLines[srcIdx])
				srcIdx++
			case '-':
				// Remove: consume source line (must match)
				if srcIdx >= len(srcLines) {
					return "", fmt.Errorf("hunk %d: remove line %q past end of source", hi+1, hl.text)
				}
				if srcLines[srcIdx] != hl.text {
					return "", fmt.Errorf("hunk %d: remove mismatch at line %d: expected %q, got %q",
						hi+1, srcIdx+1, hl.text, srcLines[srcIdx])
				}
				srcIdx++
			case '+':
				// Add: emit new line
				out = append(out, hl.text)
			}
		}
	}

	// Copy remaining source lines after last hunk
	for srcIdx < len(srcLines) {
		out = append(out, srcLines[srcIdx])
		srcIdx++
	}

	return strings.Join(out, "\n"), nil
}

// --- file helpers ---

func readFileLimited(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("file size %d exceeds limit %d bytes", info.Size(), maxBytes)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// splitLines splits text into lines, stripping trailing newlines from each.
// A trailing newline on the whole string does not produce an extra empty element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// If the string ends with a newline, the last split element will be empty — remove it.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// --- shared helpers ---




