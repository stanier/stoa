package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stanier/stoa/internal/mcp/transport"
)

// Client represents an MCP client connection to a server
type Client struct {
	transport  transport.Transport
	serverInfo ServerInfo

	nextID    atomic.Int64
	pending   map[interface{}]chan *JSONRPCResponse
	pendingMu sync.RWMutex

	connected       bool
	heartbeatCancel context.CancelFunc
	mu              sync.RWMutex
}

// NewClient creates a new MCP client with the given transport
func NewClient(t transport.Transport) *Client {
	return &Client{
		transport: t,
		pending:   make(map[interface{}]chan *JSONRPCResponse),
	}
}

// Connect connects to the MCP server and performs handshake
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	// Connect transport
	if err := c.transport.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect transport: %w", err)
	}

	// Start response handler
	go c.handleResponses()

	// Send initialize request
	initParams := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities: Capabilities{
			Tools: &ToolsCapability{},
		},
		ClientInfo: ClientInfo{
			Name:    "stoa",
			Version: "0.1.0",
		},
	}

	var initResult InitializeResult
	if err := c.call(ctx, "initialize", initParams, &initResult); err != nil {
		c.transport.Close()
		return fmt.Errorf("initialize failed: %w", err)
	}

	c.serverInfo = initResult.ServerInfo
	c.connected = true

	// Send initialized notification
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	return nil
}

// Close closes the connection to the MCP server
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
		c.heartbeatCancel = nil
	}
	if !c.connected {
		return nil
	}

	c.connected = false
	return c.transport.Close()
}

// Ping sends a ping request to the server to check liveness
func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, "ping", nil, nil)
}

// StartHeartbeat starts a background goroutine that periodically pings the server.
// If a ping fails, it marks the connection as dead and calls onFailure.
func (c *Client) StartHeartbeat(ctx context.Context, interval, pingTimeout time.Duration, onFailure func()) {
	hbCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.heartbeatCancel != nil {
		c.heartbeatCancel()
	}
	c.heartbeatCancel = cancel
	c.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(hbCtx, pingTimeout)
				err := c.Ping(pingCtx)
				pingCancel()
				if err != nil {
					c.mu.Lock()
					c.connected = false
					c.mu.Unlock()
					if onFailure != nil {
						go onFailure()
					}
					return
				}
			}
		}
	}()
}

// ListTools retrieves the list of available tools from the server
func (c *Client) ListTools(ctx context.Context) ([]ToolDescription, error) {
	var result ListToolsResult
	if err := c.call(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}

	return result.Tools, nil
}

// CallTool invokes a tool on the server
func (c *Client) CallTool(ctx context.Context, params CallToolParams) (*CallToolResult, error) {
	var result CallToolResult
	if err := c.call(ctx, "tools/call", params, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// call makes a JSON-RPC call and waits for the response
func (c *Client) call(ctx context.Context, method string, params interface{}, result interface{}) error {
	// Generate request ID
	id := c.nextID.Add(1)

	// Create request
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Marshal request
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create response channel
	respChan := make(chan *JSONRPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = respChan
	c.pendingMu.Unlock()

	// Cleanup on return
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	// Send request
	if err := c.transport.Send(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-respChan:
		if resp == nil {
			return fmt.Errorf("server process exited unexpectedly")
		}
		if resp.Error != nil {
			return fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}

		// Unmarshal result if provided
		if result != nil && resp.Result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}
		}

		return nil
	}
}

// notify sends a JSON-RPC notification (no response expected)
func (c *Client) notify(ctx context.Context, method string, params interface{}) error {
	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	return c.transport.Send(data)
}

// handleResponses reads responses from the transport and dispatches them.
// When the transport closes (e.g. the subprocess exits), it drains all pending
// callers by sending a nil response so they fail immediately rather than
// blocking until the context deadline.
func (c *Client) handleResponses() {
	for {
		data, err := c.transport.Receive()
		if err != nil {
			// Transport closed — notify all pending callers so they fail fast
			// instead of waiting for the context timeout.
			c.pendingMu.Lock()
			for _, ch := range c.pending {
				select {
				case ch <- nil:
				default:
				}
			}
			c.pendingMu.Unlock()
			return
		}

		// Parse response
		var resp JSONRPCResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			// Skip malformed responses
			continue
		}

		// Normalize ID type: JSON unmarshals numbers as float64, but we store as int64
		var normalizedID interface{} = resp.ID
		if floatID, ok := resp.ID.(float64); ok {
			normalizedID = int64(floatID)
		}

		// Dispatch to waiting caller
		c.pendingMu.RLock()
		respChan, ok := c.pending[normalizedID]
		c.pendingMu.RUnlock()

		if ok {
			select {
			case respChan <- &resp:
			default:
				// Channel full, skip
			}
		}
	}
}

// ServerInfo returns information about the connected server
func (c *Client) ServerInfo() ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverInfo
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}
