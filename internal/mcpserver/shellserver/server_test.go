package shellserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkDir(t *testing.T) {
	s := NewServer("mcp-shell", "test", WithWorkspaceRoot("/workspace"))
	cases := map[string]string{
		"":                 "/workspace",
		".":                "/workspace",
		"proj":             "/workspace/proj",
		"proj/../other":    "/workspace/other",
		"..":               "/workspace",
		"../etc":           "/workspace",
		"../workspace2":    "/workspace", // shares the root's name as a prefix
		"../workspace2/x":  "/workspace",
		"a/../../..":       "/workspace",
		"/etc":             "/workspace",
		"/workspace/inner": "/workspace",
	}
	for in, want := range cases {
		if got := s.resolveWorkDir(in); got != want {
			t.Errorf("resolveWorkDir(%q) = %q, want %q", in, got, want)
		}
	}
	// A root given with a trailing slash behaves the same.
	s2 := NewServer("mcp-shell", "test", WithWorkspaceRoot("/workspace/"))
	if got := s2.resolveWorkDir("../workspace2"); got != "/workspace" {
		t.Errorf("trailing-slash root: got %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewServer("mcp-shell", "test", WithWorkspaceRoot(root), WithMaxOutputBytes(64))
	call := func(args map[string]interface{}) (string, bool) {
		res, err := s.CallTool(context.Background(), "run_command", args)
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].Text, res.IsError
	}

	text, isErr := call(map[string]interface{}{"command": "pwd", "working_dir": "../workspace2"})
	if isErr || !strings.Contains(text, "stdout:\n"+root+"\n") {
		t.Errorf("pwd: isErr=%v text=%q", isErr, text)
	}
	if _, err := os.Stat(filepath.Join(base, "workspace2")); err == nil {
		t.Error("working_dir outside the workspace was created")
	}

	text, _ = call(map[string]interface{}{"command": "false"})
	if !strings.HasSuffix(text, "exit_code: 1") {
		t.Errorf("false: %q", text)
	}

	text, _ = call(map[string]interface{}{"command": "head -c 500 /dev/zero"})
	if !strings.Contains(text, "output truncated") {
		t.Errorf("output limit not applied: %q", text)
	}

	if _, isErr := call(map[string]interface{}{"command": ""}); !isErr {
		t.Error("empty command should be an error")
	}
}
