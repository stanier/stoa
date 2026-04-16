package mcpserver

import (
	"context"
	"encoding/json"
)

// BaseServer provides a default Initialize() implementation for MCP servers.
// Embed this in your server struct and set Name/Version at construction time.
type BaseServer struct {
	Name    string
	Version string
}

// Initialize handles the MCP initialize handshake.
func (b *BaseServer) Initialize(_ context.Context, _ InitializeParams) (*InitializeResult, error) {
	return &InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]interface{}{"tools": map[string]interface{}{}},
		ServerInfo:      ServerInfo{Name: b.Name, Version: b.Version},
	}, nil
}

// Server represents an MCP server
type Server interface {
	// Initialize handles the initialize request
	Initialize(ctx context.Context, params InitializeParams) (*InitializeResult, error)

	// ListTools returns all available tools
	ListTools(ctx context.Context) ([]Tool, error)

	// CallTool executes a tool by name
	CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error)

	// Shutdown cleans up resources
	Shutdown(ctx context.Context) error
}

// Tool represents an MCP tool
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolHandler is a function that executes a tool
type ToolHandler func(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error)

// InitializeParams contains initialization parameters
type InitializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      ClientInfo             `json:"clientInfo"`
}

// InitializeResult contains initialization response
type InitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      ServerInfo             `json:"serverInfo"`
}

// ClientInfo contains client information
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerInfo contains server information
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CallToolResult contains tool execution result
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a content block in the response
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id,omitempty"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}
