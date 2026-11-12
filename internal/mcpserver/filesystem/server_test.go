package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup makes root/ and a sibling outside/ holding a secret, and returns
// a server rooted at root.
func setup(t *testing.T) (*Server, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside, filepath.Join(base, "root2")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(root, "mcp-filesystem", "test")
	if err != nil {
		t.Fatal(err)
	}
	return s, s.rootDir, outside
}

func call(t *testing.T, s *Server, tool string, args map[string]interface{}) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	return res.Content[0].Text, res.IsError
}

func TestReadWriteInsideRoot(t *testing.T) {
	s, root, _ := setup(t)
	if text, isErr := call(t, s, "read_file", map[string]interface{}{"path": "hello.txt"}); isErr || !strings.Contains(text, "hello world") {
		t.Fatalf("read: isErr=%v text=%q", isErr, text)
	}
	if _, isErr := call(t, s, "write_file", map[string]interface{}{"path": "sub/dir/new.txt", "content": "hi"}); isErr {
		t.Fatal("write to new nested path failed")
	}
	if b, err := os.ReadFile(filepath.Join(root, "sub/dir/new.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("written file: %q %v", b, err)
	}
	if _, isErr := call(t, s, "read_file", map[string]interface{}{"path": filepath.Join(root, "hello.txt")}); isErr {
		t.Fatal("absolute path inside root should be allowed")
	}
}

func TestTraversalRejected(t *testing.T) {
	s, _, outside := setup(t)
	for _, p := range []string{
		"../outside/secret.txt",
		"sub/../../outside/secret.txt",
		filepath.Join(outside, "secret.txt"),
		"../root2/x.txt", // sibling sharing the root's name as a prefix
		"/etc/passwd",
	} {
		if text, isErr := call(t, s, "read_file", map[string]interface{}{"path": p}); !isErr || strings.Contains(text, "TOPSECRET") {
			t.Errorf("read %q: isErr=%v text=%q", p, isErr, text)
		}
		if _, isErr := call(t, s, "write_file", map[string]interface{}{"path": p, "content": "x"}); !isErr {
			t.Errorf("write %q succeeded", p)
		}
	}
}

func TestSymlinkEscapes(t *testing.T) {
	s, root, outside := setup(t)
	mustSymlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt"))
	mustSymlink(t, outside, filepath.Join(root, "linkdir"))
	mustSymlink(t, filepath.Join(outside, "nothing-yet.txt"), filepath.Join(root, "dangling.txt"))

	if text, isErr := call(t, s, "read_file", map[string]interface{}{"path": "link.txt"}); !isErr || strings.Contains(text, "TOPSECRET") {
		t.Errorf("read through file symlink: isErr=%v text=%q", isErr, text)
	}
	if _, isErr := call(t, s, "list_directory", map[string]interface{}{"path": "linkdir"}); !isErr {
		t.Error("list through dir symlink succeeded")
	}
	// Writing a new file under a symlinked directory must not land outside.
	if _, isErr := call(t, s, "write_file", map[string]interface{}{"path": "linkdir/new.txt", "content": "x"}); !isErr {
		t.Error("write through dir symlink succeeded")
	}
	if _, isErr := call(t, s, "write_file", map[string]interface{}{"path": "linkdir/a/b/new.txt", "content": "x"}); !isErr {
		t.Error("nested write through dir symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Error("file was created outside the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "a")); err == nil {
		t.Error("directory was created outside the root")
	}
	// A dangling link would create its target on write.
	if _, isErr := call(t, s, "write_file", map[string]interface{}{"path": "dangling.txt", "content": "x"}); !isErr {
		t.Error("write through dangling symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "nothing-yet.txt")); err == nil {
		t.Error("dangling symlink target was created")
	}
}

func TestSearchContentSkipsOutsideSymlinks(t *testing.T) {
	s, root, outside := setup(t)
	mustSymlink(t, filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt"))
	mustSymlink(t, filepath.Join(root, "hello.txt"), filepath.Join(root, "inner-link.txt"))

	text, isErr := call(t, s, "search_content", map[string]interface{}{"query": "TOPSECRET"})
	if isErr || strings.Contains(text, "link.txt") || !strings.Contains(text, "Found 0") {
		t.Errorf("search found content outside the root: isErr=%v text=%q", isErr, text)
	}
	text, isErr = call(t, s, "search_content", map[string]interface{}{"query": "hello"})
	if isErr || !strings.Contains(text, "inner-link.txt") {
		t.Errorf("search should still read links inside the root: isErr=%v text=%q", isErr, text)
	}
}

func TestRootBehindSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "f.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, real, filepath.Join(base, "alias"))
	s, err := NewServer(filepath.Join(base, "alias"), "mcp-filesystem", "test")
	if err != nil {
		t.Fatal(err)
	}
	if text, isErr := call(t, s, "read_file", map[string]interface{}{"path": "f.txt"}); isErr || !strings.Contains(text, "ok") {
		t.Fatalf("read under symlinked root: isErr=%v text=%q", isErr, text)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}
