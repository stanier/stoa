// Package shellserver implements an MCP server that executes shell commands.
// It is designed to run as a long-lived container process; commands are executed
// via os/exec using the container's own shell. The workspace root is /workspace.
package shellserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Server implements mcpserver.Server for shell command execution.
type Server struct {
	mcpserver.BaseServer
	workspaceRoot  string
	defaultTimeout int // seconds
	maxOutputBytes int
	handlers       map[string]mcpserver.ToolHandler
}

// NewServer creates a new shell MCP server.
func NewServer(name, version string, opts ...Option) *Server {
	s := &Server{
		BaseServer:    mcpserver.BaseServer{Name: name, Version: version},
		workspaceRoot: "/workspace",
		defaultTimeout: 60,
		maxOutputBytes: 51200,
	}
	for _, o := range opts {
		o(s)
	}
	s.handlers = map[string]mcpserver.ToolHandler{
		"run_command": s.handleRunCommand,
	}
	return s
}

// Option is a functional option for Server.
type Option func(*Server)

func WithWorkspaceRoot(root string) Option {
	return func(s *Server) { s.workspaceRoot = root }
}

func WithDefaultTimeout(secs int) Option {
	return func(s *Server) { s.defaultTimeout = secs }
}

func WithMaxOutputBytes(n int) Option {
	return func(s *Server) { s.maxOutputBytes = n }
}

// --- mcpserver.Server interface ---

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name: "run_command",
			Description: "Execute a shell command inside the sandbox container. " +
				"The workspace directory /workspace is persistent within this container's lifetime. " +
				"Use working_dir to specify where to run the command (relative to /workspace or absolute). " +
				"stdout, stderr, and exit_code are returned.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to run (passed to sh -c).",
					},
					"working_dir": map[string]interface{}{
						"type":        "string",
						"description": "Working directory. Relative paths are resolved within /workspace. Defaults to /workspace.",
					},
					"timeout": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Timeout in seconds (default %d, max 300).", s.defaultTimeout),
					},
				},
				"required": []string{"command"},
			},
		},
	}, nil
}

func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

func (s *Server) Shutdown(_ context.Context) error { return nil }

// --- handlers ---

func (s *Server) handleRunCommand(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	command := mcpserver.StrArg(args, "command", "")
	if command == "" {
		return mcpserver.ErrorResult("command is required"), nil
	}

	workDir := s.resolveWorkDir(mcpserver.StrArg(args, "working_dir", ""))
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to create working directory %q: %v", workDir, err)), nil
	}

	timeout := mcpserver.IntArg(args, "timeout", s.defaultTimeout)
	if timeout <= 0 || timeout > 300 {
		timeout = s.defaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Split the command into argv to avoid shell injection. Shell metacharacters
	// (pipes, redirects, variable expansion, etc.) are not interpreted.
	// Complex shell pipelines are intentionally unsupported.
	argv := strings.Fields(command)
	if len(argv) == 0 {
		return mcpserver.ErrorResult("command is empty after parsing"), nil
	}
	cmd := exec.CommandContext(execCtx, argv[0], argv[1:]...)
	cmd.Dir = workDir

	var stdoutBuf, stderrBuf bytes.Buffer
	lw := &limitedWriter{limit: s.maxOutputBytes}
	cmd.Stdout = &limitedSink{lw: lw, buf: &stdoutBuf}
	cmd.Stderr = &limitedSink{lw: lw, buf: &stderrBuf}

	runErr := cmd.Run()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() == context.DeadlineExceeded {
			exitCode = 124 // timeout exit code (same as GNU timeout)
		} else {
			exitCode = 1
		}
	}

	return mcpserver.TextResult(formatResult(stdoutBuf.String(), stderrBuf.String(), exitCode, lw.truncated)), nil
}

// resolveWorkDir converts working_dir argument to an absolute path inside the workspace.
// Absolute paths are rejected — callers must use paths relative to the workspace root.
func (s *Server) resolveWorkDir(workDir string) string {
	if workDir == "" {
		return s.workspaceRoot
	}
	if filepath.IsAbs(workDir) {
		// Reject absolute paths; fall back to workspace root.
		return s.workspaceRoot
	}
	// Relative path — resolve within workspace.
	clean := filepath.Clean(filepath.Join(s.workspaceRoot, workDir))
	// Prevent path traversal above the workspace root.
	if !strings.HasPrefix(clean, s.workspaceRoot) {
		return s.workspaceRoot
	}
	return clean
}

// --- output formatting ---

func formatResult(stdout, stderr string, exitCode int, truncated bool) string {
	var sb strings.Builder
	if stdout != "" {
		sb.WriteString("stdout:\n")
		sb.WriteString(stdout)
	}
	if stderr != "" {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("stderr:\n")
		sb.WriteString(stderr)
	}
	if truncated {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("[output truncated — exceeded max_output_bytes]\n")
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("exit_code: %d", exitCode))
	return sb.String()
}

// --- helpers ---





// limitedWriter tracks total bytes written across stdout and stderr.
type limitedWriter struct {
	limit     int
	written   int
	truncated bool
}

// limitedSink wraps a bytes.Buffer with a shared limitedWriter.
type limitedSink struct {
	lw  *limitedWriter
	buf *bytes.Buffer
}

func (ls *limitedSink) Write(p []byte) (int, error) {
	if ls.lw.truncated {
		return len(p), nil
	}
	remaining := ls.lw.limit - ls.lw.written
	if remaining <= 0 {
		ls.lw.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		ls.lw.truncated = true
	}
	n, err := ls.buf.Write(p)
	ls.lw.written += n
	// Always return original len(p) to avoid breaking io.Writer callers.
	if err == nil || err == io.ErrShortWrite {
		return len(p), nil
	}
	return n, err
}
