package discord

import "strings"

const DefaultMessageLimit = 2000

// SplitMessage splits text into chunks no longer than limit characters.
// It respects code-block boundaries (``` delimited): if a split point falls
// inside a code block the split is pushed back to before the opening ``` and
// the block is closed/reopened across the boundary.
// Outside code blocks it prefers paragraph boundaries (\n\n), then newlines,
// then spaces. It always returns at least one string and never returns an
// empty slice.
func SplitMessage(text string, limit int) []string {
	if limit <= 0 {
		limit = DefaultMessageLimit
	}
	if len(text) <= limit {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= limit {
			chunks = append(chunks, remaining)
			break
		}

		chunk, rest := cutChunk(remaining, limit)
		chunks = append(chunks, chunk)
		remaining = rest
	}

	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// cutChunk cuts the first chunk of at most limit bytes from text, aware of
// code blocks. Returns (chunk, remainder).
func cutChunk(text string, limit int) (string, string) {
	// Find the position of every ``` in the first limit+1 runes so we know
	// whether the cut point is inside a code block.
	cutPos := findSplitPoint(text, limit)
	chunk := text[:cutPos]
	rest := text[cutPos:]

	// Count ``` occurrences in chunk to determine if we're inside a code block.
	openBlocks := strings.Count(chunk, "```")
	if openBlocks%2 == 0 {
		// Even count: we're outside a code block. Return as-is.
		return strings.TrimRight(chunk, " "), strings.TrimLeft(rest, " \n")
	}

	// Odd count: the cut landed inside a code block.
	// Find the last ``` before cutPos to determine the fence and push the
	// split back to just before it.
	lastFence := strings.LastIndex(chunk, "```")
	if lastFence <= 0 {
		// Degenerate: can't push back further; hard-cut.
		return chunk, rest
	}

	// Extract the fence language hint (e.g. "```go\n" → "go").
	fenceLine := chunk[lastFence:]
	lang := ""
	if nl := strings.IndexByte(fenceLine, '\n'); nl > 3 {
		lang = strings.TrimSpace(fenceLine[3:nl])
	}

	// Split just before the opening ```.
	beforeFence := strings.TrimRight(chunk[:lastFence], " \n")
	inBlock := text[lastFence+cutPos-len(chunk):]
	// Re-open the block at the start of the next chunk.
	reopen := "```" + lang + "\n"

	return beforeFence, reopen + inBlock
}

// findSplitPoint returns the best byte index ≤ limit at which to split text,
// preferring paragraph breaks, then newlines, then spaces.
func findSplitPoint(text string, limit int) int {
	if len(text) <= limit {
		return len(text)
	}

	// Ensure we don't cut in the middle of a multi-byte UTF-8 sequence.
	end := limit
	for end > 0 && isRuneContinuation(text[end]) {
		end--
	}

	// Prefer \n\n (paragraph break).
	if idx := strings.LastIndex(text[:end], "\n\n"); idx > 0 {
		return idx + 2 // include the blank line
	}

	// Prefer \n.
	if idx := strings.LastIndex(text[:end], "\n"); idx > 0 {
		return idx + 1
	}

	// Prefer space.
	if idx := strings.LastIndex(text[:end], " "); idx > 0 {
		return idx + 1
	}

	// Hard cut.
	return end
}

// isRuneContinuation reports whether b is a UTF-8 continuation byte.
func isRuneContinuation(b byte) bool {
	return b&0xC0 == 0x80
}
