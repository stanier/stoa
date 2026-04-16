package agentproto

import (
	"strings"
	"testing"
)

func TestFormatTokensResponseBasic(t *testing.T) {
	resp := TokensResponse{
		TotalTokens:   1000,
		TotalMessages: 5,
	}
	out := FormatTokensResponse(resp)
	if !strings.Contains(out, "1000") {
		t.Errorf("expected total tokens in output, got: %q", out)
	}
	if !strings.Contains(out, "5") {
		t.Errorf("expected message count in output, got: %q", out)
	}
}

func TestFormatTokensResponseByRole(t *testing.T) {
	resp := TokensResponse{
		TotalTokens:   900,
		TotalMessages: 3,
		ByRole: map[string]int{
			"system":    500,
			"user":      200,
			"assistant": 200,
			"summary":   0,
		},
	}
	out := FormatTokensResponse(resp)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in output, got: %q", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("expected system token count in output, got: %q", out)
	}
	// Roles with 0 tokens must not appear
	if strings.Contains(out, "summary") {
		t.Errorf("expected zero-count role 'summary' to be omitted, got: %q", out)
	}
}

func TestFormatTokensResponseNoByRole(t *testing.T) {
	resp := TokensResponse{TotalTokens: 42, TotalMessages: 1}
	out := FormatTokensResponse(resp)
	if strings.Contains(out, "By role") {
		t.Errorf("expected no 'By role' section when ByRole is empty, got: %q", out)
	}
}
