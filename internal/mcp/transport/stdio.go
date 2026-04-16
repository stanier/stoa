package transport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// StdioTransport implements Transport using stdio (subprocess)
type StdioTransport struct {
	command string
	args    []string
	env     map[string]string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	reader *bufio.Reader
	mu     sync.Mutex
}

// NewStdioTransport creates a new stdio transport
func NewStdioTransport(command string, args []string, env map[string]string) *StdioTransport {
	return &StdioTransport{
		command: command,
		args:    args,
		env:     env,
	}
}

// Connect starts the subprocess and connects to its stdio
func (t *StdioTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cmd != nil {
		return fmt.Errorf("already connected")
	}

	// Create command with background context (process should live beyond connection timeout)
	// The connection timeout is only for initialization, not for the subprocess lifetime
	t.cmd = exec.Command(t.command, t.args...)

	// Set environment variables
	if len(t.env) > 0 {
		t.cmd.Env = append(t.cmd.Environ(), mapToEnvSlice(t.env)...)
	}

	// Get pipes
	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	t.stderr, err = t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	// Create buffered reader for stdout
	t.reader = bufio.NewReader(t.stdout)

	// Start the process
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Start goroutine to consume stderr (prevent blocking)
	go func() {
		// Discard stderr to prevent pipe blocking
		io.Copy(io.Discard, t.stderr)
	}()

	return nil
}

// Close terminates the subprocess
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cmd == nil {
		return nil
	}

	// Close stdin to signal EOF
	if t.stdin != nil {
		t.stdin.Close()
	}

	// Wait for process to exit
	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
		t.cmd.Wait()
	}

	t.cmd = nil
	return nil
}

// Send writes data to the subprocess stdin
func (t *StdioTransport) Send(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stdin == nil {
		return fmt.Errorf("not connected")
	}

	// Write data followed by newline (JSON-RPC expects line-delimited JSON)
	_, err := t.stdin.Write(append(data, '\n'))
	return err
}

// Receive reads a line from the subprocess stdout
func (t *StdioTransport) Receive() ([]byte, error) {
	if t.reader == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Read until newline
	line, err := t.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	return line, nil
}

// Reader returns the stdout reader
func (t *StdioTransport) Reader() io.Reader {
	return t.stdout
}

// Writer returns the stdin writer
func (t *StdioTransport) Writer() io.Writer {
	return t.stdin
}

// mapToEnvSlice converts a map to environment variable slice
func mapToEnvSlice(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
