package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// StdioServer handles JSON-RPC communication over stdio
type StdioServer struct {
	server      Server
	reader      *bufio.Reader
	writer      *bufio.Writer
	pingTimeout time.Duration
	pingCh      chan struct{}
	// watchdogArmCh is closed once notifications/initialized is received,
	// which arms the ping watchdog. The watchdog must not start before the
	// MCP handshake completes — the client only begins pinging after that.
	watchdogArmCh   chan struct{}
	watchdogArmOnce sync.Once
}

// NewStdioServer creates a new stdio server
func NewStdioServer(server Server) *StdioServer {
	return &StdioServer{
		server:        server,
		reader:        bufio.NewReader(os.Stdin),
		writer:        bufio.NewWriter(os.Stdout),
		pingCh:        make(chan struct{}, 1),
		watchdogArmCh: make(chan struct{}),
	}
}

// SetPingTimeout enables a watchdog that shuts down the server if no ping is
// received within d. Pass 0 (the default) to disable. When the watchdog fires
// it calls os.Exit(1) directly because ReadBytes blocks stdin and cannot be
// interrupted through context cancellation alone.
func (s *StdioServer) SetPingTimeout(d time.Duration) {
	s.pingTimeout = d
}

// Run starts the server loop
func (s *StdioServer) Run(ctx context.Context) error {
	if s.pingTimeout > 0 {
		go func() {
			// Wait until the MCP handshake is complete (notifications/initialized)
			// before arming the watchdog. The client only starts pinging after
			// initialization, so counting from server startup would always fire
			// before the first ping arrives.
			select {
			case <-ctx.Done():
				return
			case <-s.watchdogArmCh:
				// handshake complete, proceed
			}

			log.Printf("Ping watchdog armed: will shut down if no ping within %v", s.pingTimeout)
			timer := time.NewTimer(s.pingTimeout)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					log.Printf("Ping watchdog: no ping received within %v, shutting down", s.pingTimeout)
					os.Exit(1)
				case <-s.pingCh:
					// Reset the timer on each ping
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(s.pingTimeout)
				}
			}
		}()
	}

	defer s.writer.Flush()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Read request
			line, err := s.reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("read error: %w", err)
			}

			// Parse request
			var req JSONRPCRequest
			if err := json.Unmarshal(line, &req); err != nil {
				s.sendError(nil, -32700, fmt.Sprintf("Parse error: %v", err))
				continue
			}

			// Handle request
			s.handleRequest(ctx, &req)
		}
	}
}

// handleRequest processes a JSON-RPC request
func (s *StdioServer) handleRequest(ctx context.Context, req *JSONRPCRequest) {
	log.Printf("Received request: method=%s id=%v", req.Method, req.ID)

	switch req.Method {
	case "initialize":
		s.handleInitialize(ctx, req)
	case "notifications/initialized":
		// No response needed for notifications
		log.Println("Client initialized")
		// Arm the ping watchdog now that the handshake is complete
		s.watchdogArmOnce.Do(func() { close(s.watchdogArmCh) })
	case "tools/list":
		s.handleToolsList(ctx, req)
	case "tools/call":
		s.handleToolsCall(ctx, req)
	case "ping":
		s.sendResponse(req.ID, struct{}{})
		select {
		case s.pingCh <- struct{}{}:
		default:
		}
	default:
		s.sendError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

// handleInitialize handles the initialize request
func (s *StdioServer) handleInitialize(ctx context.Context, req *JSONRPCRequest) {
	var params InitializeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, fmt.Sprintf("Invalid params: %v", err))
		return
	}

	result, err := s.server.Initialize(ctx, params)
	if err != nil {
		s.sendError(req.ID, -32603, fmt.Sprintf("Internal error: %v", err))
		return
	}

	s.sendResponse(req.ID, result)
}

// handleToolsList handles the tools/list request
func (s *StdioServer) handleToolsList(ctx context.Context, req *JSONRPCRequest) {
	tools, err := s.server.ListTools(ctx)
	if err != nil {
		s.sendError(req.ID, -32603, fmt.Sprintf("Internal error: %v", err))
		return
	}

	result := map[string]interface{}{
		"tools": tools,
	}
	s.sendResponse(req.ID, result)
}

// handleToolsCall handles the tools/call request
func (s *StdioServer) handleToolsCall(ctx context.Context, req *JSONRPCRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, fmt.Sprintf("Invalid params: %v", err))
		return
	}

	result, err := s.server.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		s.sendError(req.ID, -32603, fmt.Sprintf("Internal error: %v", err))
		return
	}

	s.sendResponse(req.ID, result)
}

// sendResponse sends a JSON-RPC response
func (s *StdioServer) sendResponse(id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error marshaling response: %v", err)
		return
	}

	s.writer.Write(data)
	s.writer.WriteByte('\n')
	s.writer.Flush()

	log.Printf("Sent response: id=%v", id)
}

// sendError sends a JSON-RPC error response
func (s *StdioServer) sendError(id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error marshaling error response: %v", err)
		return
	}

	s.writer.Write(data)
	s.writer.WriteByte('\n')
	s.writer.Flush()

	log.Printf("Sent error: id=%v code=%d message=%s", id, code, message)
}
