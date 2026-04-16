package agentproto

import (
	"fmt"
	"strings"
)

// FormatTokensResponse formats a TokensResponse for human-readable display.
// The output is plain text suitable for both TUI and Zulip (markdown) contexts.
//
// Example output:
//
//	Total tokens: 14,832  |  Messages: 12
//	By role:
//	  system:    8,200 tokens
//	  user:      3,100 tokens
//	  assistant: 3,532 tokens
func FormatTokensResponse(resp TokensResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Total tokens: %d  |  Messages: %d", resp.TotalTokens, resp.TotalMessages)

	if len(resp.ByRole) > 0 {
		// Real API counts: show input/output split, then per-role char estimates.
		if input, ok := resp.ByRole["input"]; ok {
			output := resp.ByRole["output"]
			fmt.Fprintf(&b, "\n  %-10s %d tokens (includes system prompt + tool schemas)", "input:", input)
			fmt.Fprintf(&b, "\n  %-10s %d tokens", "output:", output)
			b.WriteString("\nApprox by role (char estimate):")
			for _, role := range []string{"system", "user", "assistant", "summary"} {
				if n, ok := resp.ByRole[role]; ok && n > 0 {
					fmt.Fprintf(&b, "\n  %-10s %d tokens", role+":", n)
				}
			}
		} else {
			// Char estimate only (pre-first-turn fallback).
			b.WriteString("\nBy role:")
			for _, role := range []string{"system", "user", "assistant", "summary"} {
				if n, ok := resp.ByRole[role]; ok && n > 0 {
					fmt.Fprintf(&b, "\n  %-10s %d tokens", role+":", n)
				}
			}
		}
	}

	return b.String()
}
