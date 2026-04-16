package agentd

import (
	"os"
	"time"

	"github.com/stanier/stoa/internal/chat"
)

const (
	// AgenticLoopTimeout is the maximum duration for a single agentic loop turn.
	AgenticLoopTimeout = 5 * time.Minute
	// MCPConnectTimeout is the timeout for MCP server connection during startup.
	MCPConnectTimeout = 30 * time.Second
	// MaxIterations is the maximum number of LLM→tool→LLM iterations per user message.
	MaxIterations = 10
)

// internal aliases so the rest of the package can use the old unqualified names.
const (
	agenticLoopTimeout = AgenticLoopTimeout
	mcpConnectTimeout  = MCPConnectTimeout
	maxIterations      = MaxIterations
)

// systemMessage creates a system chat.Message from the given prompt text.
func systemMessage(prompt string) chat.Message {
	return chat.NewSystemMessage(prompt)
}

// envOr returns the value of the named environment variable, or def if unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
