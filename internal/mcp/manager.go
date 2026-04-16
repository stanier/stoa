package mcp

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/mcp/transport"
	"github.com/stanier/stoa/internal/skills"
)

// ServerManager manages multiple MCP server connections
type ServerManager struct {
	servers        map[string]*ServerConnection
	proxyAddress   string // HTTP proxy URL for containers with AllowedDomains
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	reconnectChans map[string]chan struct{} // per-server signal channel for reconnect loop
}

// ServerConnection represents a connection to an MCP server
type ServerConnection struct {
	Config config.MCPServerConfig
	Client *Client
	Tools  []ToolDescription
}

// NewServerManager creates a new server manager
func NewServerManager(mcpCfg config.MCPConfig) *ServerManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &ServerManager{
		servers:        make(map[string]*ServerConnection),
		proxyAddress:   mcpCfg.ProxyAddress,
		ctx:            ctx,
		cancel:         cancel,
		reconnectChans: make(map[string]chan struct{}),
	}

	for _, cfg := range mcpCfg.Servers {
		if cfg.Enabled != nil && !*cfg.Enabled {
			continue
		}
		sm.servers[cfg.Name] = &ServerConnection{
			Config: cfg,
		}
		ch := make(chan struct{}, 1)
		sm.reconnectChans[cfg.Name] = ch
		go sm.reconnectLoop(cfg.Name, ch)
	}

	return sm
}

// ConnectAll connects to all configured servers
func (sm *ServerManager) ConnectAll(ctx context.Context) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var errors []error

	for name, conn := range sm.servers {
		if err := sm.connectServer(ctx, name, conn); err != nil {
			logging.Warnf("MCP server %q: connect failed: %v", name, err)
			errors = append(errors, fmt.Errorf("%s: %w", name, err))
		} else {
			logging.Infof("MCP server %q: connected", name)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to connect to some servers: %v", errors)
	}

	return nil
}

// connectServer connects to a single server
func (sm *ServerManager) connectServer(ctx context.Context, name string, conn *ServerConnection) error {
	// Expand ${VAR} references in args and env values using agentd's environment.
	// This lets config.yaml reference env vars (e.g. ${PROMETHEUS_BASE_URL}) that
	// are injected via docker-compose, keeping secrets out of the config file.
	args := expandStringSlice(conn.Config.Args)
	env := expandStringMap(conn.Config.Env)

	// Create transport based on config
	var t transport.Transport
	switch conn.Config.Transport {
	case "stdio":
		t = transport.NewStdioTransport(conn.Config.Command, args, env)
	case "http":
		t = transport.NewHTTPTransport(conn.Config.URL)
	case "docker":
		// Inject proxy env vars for containers on an isolated per-container network.
		// The container has no direct internet route; HTTP/HTTPS goes through squid,
		// which handles DNS resolution itself.  We do NOT override the container's
		// DNS — Docker's embedded resolver (127.0.0.11) must remain active so the
		// container can resolve "stoa-squid" to reach the proxy.
		if conn.Config.ProxySubnet != "" && sm.proxyAddress != "" {
			if env == nil {
				env = make(map[string]string)
			}
			env["HTTP_PROXY"] = sm.proxyAddress
			env["HTTPS_PROXY"] = sm.proxyAddress
			env["NO_PROXY"] = "localhost,127.0.0.1"
		}
		cfg := transport.DockerTransportConfig{
			Image:         conn.Config.Image,
			ContainerName: "stoa-mcp-" + transport.SanitizeContainerName(conn.Config.Name),
			Args:          args,
			Env:           env,
			Volumes:       convertVolumeConfigs(conn.Config.Volumes),
			NetworkMode:   conn.Config.NetworkMode,
			DockerHost:    conn.Config.DockerHost,
		}
		var err error
		t, err = transport.NewDockerTransport(cfg)
		if err != nil {
			return fmt.Errorf("docker transport: %w", err)
		}
	default:
		return fmt.Errorf("unknown transport type: %s", conn.Config.Transport)
	}

	// Create client
	client := NewClient(t)

	// Connect
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	conn.Client = client

	// Probe ping support; only enable heartbeat if server responds
	probeCtx, probeCancel := context.WithTimeout(sm.ctx, 3*time.Second)
	pingErr := client.Ping(probeCtx)
	probeCancel()
	if pingErr != nil {
		logging.Warnf("MCP server %q: ping not supported (%v), heartbeat disabled", name, pingErr)
	} else {
		client.StartHeartbeat(sm.ctx, 30*time.Second, 5*time.Second, func() {
			logging.Warnf("MCP server %q: heartbeat failed, scheduling reconnect", name)
			sm.scheduleReconnect(name)
		})
	}

	return nil
}

// scheduleReconnect signals the per-server reconnect loop to start a reconnect attempt.
// It is non-blocking: if a reconnect is already pending, the signal is dropped.
func (sm *ServerManager) scheduleReconnect(name string) {
	ch, ok := sm.reconnectChans[name]
	if !ok {
		return
	}
	select {
	case ch <- struct{}{}:
	default: // reconnect already pending, drop
	}
}

// reconnectLoop runs for the lifetime of the ServerManager, waiting for reconnect
// signals and retrying with exponential backoff. One goroutine per server.
func (sm *ServerManager) reconnectLoop(name string, trigger <-chan struct{}) {
	const maxBackoff = 5 * time.Minute
	for {
		// Wait for a reconnect signal or shutdown.
		select {
		case <-sm.ctx.Done():
			return
		case <-trigger:
		}

		backoff := time.Second
		for {
			select {
			case <-sm.ctx.Done():
				return
			case <-time.After(backoff):
			}

			sm.mu.Lock()
			conn, ok := sm.servers[name]
			var oldClient *Client
			if ok && conn.Client != nil {
				oldClient = conn.Client
				conn.Client = nil
			}
			sm.mu.Unlock()

			if !ok {
				return
			}

			if oldClient != nil {
				_ = oldClient.Close()
			}

			sm.mu.Lock()
			err := sm.connectServer(sm.ctx, name, conn)
			sm.mu.Unlock()

			if err == nil {
				logging.Infof("MCP server %q: reconnected successfully", name)
				backoff = time.Second // reset for next failure
				break
			}

			logging.Warnf("MCP server %q: reconnect failed (%v), retrying in %v", name, err, backoff*2)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// DiscoverTools discovers tools from all connected servers
func (sm *ServerManager) DiscoverTools(ctx context.Context) ([]skills.Tool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var allTools []skills.Tool

	for _, conn := range sm.servers {
		if conn.Client == nil {
			logging.Warnf("MCP server %q: client is nil, skipping", conn.Config.Name)
			continue
		}
		if !conn.Client.IsConnected() {
			logging.Warnf("MCP server %q: not connected, skipping", conn.Config.Name)
			continue
		}

		// List tools from this server
		tools, err := conn.Client.ListTools(ctx)
		if err != nil {
			logging.Warnf("MCP server %q: ListTools failed: %v", conn.Config.Name, err)
			continue
		}

		logging.Infof("MCP server %q: discovered %d tools", conn.Config.Name, len(tools))
		conn.Tools = tools

		// Convert MCP tools to skills.Tool adapters
		for _, toolDesc := range tools {
			adapter := NewMCPToolAdapter(conn.Client, conn.Config.Name, toolDesc)
			allTools = append(allTools, adapter)
		}
	}

	return allTools, nil
}

// GetServer returns a server connection by name
func (sm *ServerManager) GetServer(name string) (*ServerConnection, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	conn, ok := sm.servers[name]
	return conn, ok
}

// ListServers returns all server connections
func (sm *ServerManager) ListServers() map[string]*ServerConnection {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[string]*ServerConnection, len(sm.servers))
	for k, v := range sm.servers {
		result[k] = v
	}
	return result
}

// CloseAll closes all server connections
func (sm *ServerManager) CloseAll() error {
	sm.cancel() // Stop all heartbeats and reconnect goroutines first
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var errors []error

	for name, conn := range sm.servers {
		if conn.Client != nil {
			if err := conn.Client.Close(); err != nil {
				errors = append(errors, fmt.Errorf("%s: %w", name, err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to close some servers: %v", errors)
	}

	return nil
}

// expandStringSlice returns a new slice with environment variable references
// expanded. Only values that explicitly contain ${VAR} or $VAR syntax are
// expanded; plain strings are returned as-is. This prevents accidental
// expansion of values that happen to contain dollar signs.
func expandStringSlice(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = expandIfNeeded(s)
	}
	return out
}

// expandStringMap returns a new map with environment variable references in
// values expanded. Keys are never expanded.
func expandStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = expandIfNeeded(v)
	}
	return out
}

// expandIfNeeded expands environment variable references only if the string
// contains ${ or $ followed by an identifier character. This limits expansion
// to explicitly-templated values and avoids touching strings that happen to
// contain a bare dollar sign.
func expandIfNeeded(s string) string {
	for i, c := range s {
		if c == '$' && i+1 < len(s) {
			next := s[i+1]
			if next == '{' || (next >= 'A' && next <= 'Z') || (next >= 'a' && next <= 'z') || next == '_' {
				return os.ExpandEnv(s)
			}
		}
	}
	return s
}

// convertVolumeConfigs converts config.VolumeConfig to transport.VolumeConfig
func convertVolumeConfigs(configs []config.VolumeConfig) []transport.VolumeConfig {
	result := make([]transport.VolumeConfig, 0, len(configs))
	for _, cfg := range configs {
		result = append(result, transport.VolumeConfig{
			Type:     cfg.Type,
			Source:   cfg.Source,
			Target:   cfg.Target,
			ReadOnly: cfg.ReadOnly,
		})
	}
	return result
}
