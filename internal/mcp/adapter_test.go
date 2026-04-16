package mcp

import (
	"testing"
)

func TestMCPToolAdapter_Name(t *testing.T) {
	desc := ToolDescription{
		Name:        "read_file",
		Description: "Read a file",
	}
	adapter := NewMCPToolAdapter(nil, "filesystem", desc)

	expected := "mcp_filesystem_read_file"
	if adapter.Name() != expected {
		t.Errorf("expected name %q, got %q", expected, adapter.Name())
	}
}
