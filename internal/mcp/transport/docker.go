package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/strslice"
	"github.com/moby/moby/client"
)

// DockerTransportConfig holds configuration for Docker transport
type DockerTransportConfig struct {
	Image         string            // Docker image to use
	ContainerName string            // Deterministic container name; stale container with same name is force-removed on connect
	Args          []string          // CLI flags passed to the image's ENTRYPOINT (the MCP binary)
	Env           map[string]string // Environment variables
	Volumes       []VolumeConfig    // Volume mounts
	NetworkMode   string            // Network mode (bridge, host, none, or custom network name)
	DockerHost    string            // Docker/Podman socket (e.g. unix:///run/user/1000/podman/podman.sock); falls back to DOCKER_HOST env var
}

// VolumeConfig represents a Docker volume mount
type VolumeConfig struct {
	Type     string // "bind" or "volume"
	Source   string // Host path or volume name
	Target   string // Container path
	ReadOnly bool   // Read-only mount
}

// DockerTransport implements Transport by running the image's own entrypoint
// (the MCP binary) as the container's main process, with stdio attached directly.
// Args from the config become the container's Cmd (appended to the ENTRYPOINT).
// No exec session is used — the container IS the MCP server process.
type DockerTransport struct {
	cfg         DockerTransportConfig
	cli         *client.Client
	containerID string
	stdin       io.WriteCloser
	stdout      io.Reader
	reader      *bufio.Reader
	mu          sync.Mutex
}

// NewDockerTransport creates a new Docker transport
func NewDockerTransport(cfg DockerTransportConfig) (*DockerTransport, error) {
	// Create Docker client. If DockerHost is set in config, use it explicitly;
	// otherwise fall back to DOCKER_HOST env var (client.FromEnv).
	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if cfg.DockerHost != "" {
		opts = append(opts, client.WithHost(cfg.DockerHost))
	} else {
		opts = append(opts, client.FromEnv)
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &DockerTransport{
		cfg: cfg,
		cli: cli,
	}, nil
}

// Connect creates the container, attaches to its stdio, then starts it.
// Attaching before starting ensures we never miss early output from the MCP
// binary's initialization phase.
func (t *DockerTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.containerID != "" {
		return fmt.Errorf("already connected")
	}

	// Pull image if not present locally
	if err := t.pullImageIfNeeded(ctx); err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	// Create container. Args become the container Cmd (appended to the image's
	// ENTRYPOINT), so no binary path reconstruction is needed.
	containerID, err := t.createContainer(ctx)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}
	t.containerID = containerID

	// Attach to the container's stdio BEFORE starting so we don't miss any
	// output from the MCP binary's startup. With Tty:false the stream is
	// multiplexed with 8-byte headers; demuxDockerStream demuxes it below.
	attachResp, err := t.cli.ContainerAttach(ctx, containerID, client.ContainerAttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		t.cleanup(ctx)
		return fmt.Errorf("failed to attach to container: %w", err)
	}

	// Now start the container (runs the MCP binary as PID 1)
	if _, err := t.cli.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		attachResp.Close()
		t.cleanup(ctx)
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Demux the multiplexed stdout/stderr stream into a plain byte pipe.
	// Stderr from the container is forwarded to the host logger so crashes are visible.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		demuxDockerStream(pw, attachResp.Reader, t.cfg.ContainerName)
	}()

	t.stdin = attachResp.Conn
	t.stdout = pr
	t.reader = bufio.NewReader(pr)

	return nil
}

// Close stops the container and cleans up resources
func (t *DockerTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.containerID == "" {
		return nil
	}

	ctx := context.Background()

	// Close stdin to send EOF to the MCP server (triggers clean shutdown)
	if t.stdin != nil {
		t.stdin.Close()
	}

	// Stop and remove the container
	return t.cleanup(ctx)
}

// Send writes data to the container's stdin
func (t *DockerTransport) Send(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stdin == nil {
		return fmt.Errorf("not connected")
	}

	// Write data followed by newline (JSON-RPC expects line-delimited JSON)
	_, err := t.stdin.Write(append(data, '\n'))
	return err
}

// Receive reads a line from the container's stdout
func (t *DockerTransport) Receive() ([]byte, error) {
	if t.reader == nil {
		return nil, fmt.Errorf("not connected")
	}

	line, err := t.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	return line, nil
}

// Reader returns the stdout reader
func (t *DockerTransport) Reader() io.Reader {
	return t.stdout
}

// Writer returns the stdin writer
func (t *DockerTransport) Writer() io.Writer {
	return t.stdin
}

// pullImageIfNeeded pulls the Docker image if not present locally.
// Images tagged :local are expected to be built locally and are never pulled
// from a registry — a clear error is returned instead so the operator knows
// to run `make podman-build-local-mcp-all`.
func (t *DockerTransport) pullImageIfNeeded(ctx context.Context) error {
	_, err := t.cli.ImageInspect(ctx, t.cfg.Image)
	if err == nil {
		return nil // already present locally
	}

	if strings.HasSuffix(t.cfg.Image, ":local") {
		return fmt.Errorf("image %q not found locally — build it first (e.g. make podman-build-local-mcp-all)", t.cfg.Image)
	}

	resp, err := t.cli.ImagePull(ctx, t.cfg.Image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()

	_, err = io.Copy(io.Discard, resp)
	return err
}

// createContainer creates the container with the image's own ENTRYPOINT.
// Args from the config are passed as Cmd so Docker appends them to the ENTRYPOINT.
// OpenStdin/StdinOnce ensure the MCP server receives EOF when the client disconnects.
func (t *DockerTransport) createContainer(ctx context.Context) (string, error) {
	mounts := make([]mount.Mount, 0, len(t.cfg.Volumes))
	for _, vol := range t.cfg.Volumes {
		mounts = append(mounts, mount.Mount{
			Type:     mount.Type(vol.Type),
			Source:   vol.Source,
			Target:   vol.Target,
			ReadOnly: vol.ReadOnly,
		})
	}

	containerName := t.cfg.ContainerName

	// Remove any stale container with the same name (handles orphans from prior crashes).
	if containerName != "" {
		_, _ = t.cli.ContainerRemove(ctx, containerName, client.ContainerRemoveOptions{Force: true})
	}

	containerConfig := &container.Config{
		Image:     t.cfg.Image,
		Cmd:       t.cfg.Args, // appended to the image's ENTRYPOINT (the MCP binary)
		Env:       mapToEnvSlice(t.cfg.Env),
		Tty:       false,
		OpenStdin: true, // keep stdin open so MCP protocol can flow
		StdinOnce: true, // close stdin when client disconnects → server sees EOF
	}

	hostConfig := &container.HostConfig{
		Mounts:         mounts,
		NetworkMode:    container.NetworkMode(t.cfg.NetworkMode),
		AutoRemove:     true,
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp": "rw,noexec,nosuid,size=64m",
		},
		CapDrop:     strslice.StrSlice{"ALL"},
		IpcMode:     "none",
		SecurityOpt: []string{"no-new-privileges:true"},
		Resources: container.Resources{
			Memory:     128 * 1024 * 1024, // 128 MB
			MemorySwap: 128 * 1024 * 1024, // = Memory → disables swap
			NanoCPUs:   500000000,         // 0.5 CPU
			PidsLimit:  func() *int64 { v := int64(50); return &v }(),
		},
		PortBindings: network.PortMap{},
	}

	resp, err := t.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     containerConfig,
		HostConfig: hostConfig,
		Name:       containerName,
	})
	if err != nil {
		return "", err
	}

	return resp.ID, nil
}

// demuxDockerStream reads Docker's multiplexed stream format (8-byte header per frame:
// stream_type(1), padding(3), frame_size(4 big-endian)) and writes stdout frames to dst.
// Stderr frames are logged via the standard logger so container crash output is visible.
// Returns nil on clean EOF.
func demuxDockerStream(dst io.Writer, src io.Reader, containerName string) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(src, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		size := int64(binary.BigEndian.Uint32(hdr[4:8]))
		switch hdr[0] {
		case 1: // stdout
			if _, err := io.CopyN(dst, src, size); err != nil {
				return err
			}
		case 2: // stderr — log so container crash messages are visible
			buf := make([]byte, size)
			if _, err := io.ReadFull(src, buf); err != nil {
				return err
			}
			log.Printf("[%s stderr] %s", containerName, strings.TrimRight(string(buf), "\n"))
		default: // stdin echo (0) — discard
			if _, err := io.CopyN(io.Discard, src, size); err != nil {
				return err
			}
		}
	}
}

// SanitizeContainerName converts a server name to a valid Docker container name.
// Docker names allow [a-zA-Z0-9_.-]; replace anything else with "_".
func SanitizeContainerName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// cleanup stops and removes the container. "Not found" errors are silently
// ignored — the container may have already exited and been auto-removed
// (e.g., due to the ping watchdog calling os.Exit).
func (t *DockerTransport) cleanup(ctx context.Context) error {
	if t.containerID == "" {
		return nil
	}

	timeout := 10
	_, err := t.cli.ContainerStop(ctx, t.containerID, client.ContainerStopOptions{
		Timeout: &timeout,
	})
	if err != nil && cerrdefs.IsNotFound(err) {
		err = nil
	}

	t.containerID = ""
	t.stdin = nil
	t.stdout = nil
	t.reader = nil

	return err
}
