package mcp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// mockTransport is a mock transport for testing
type mockTransport struct {
	connectFunc func(ctx context.Context) error
	closeFunc   func() error
	sendFunc    func(data []byte) error
	receiveFunc func() ([]byte, error)
	readChan    chan []byte
	writeChan   chan []byte
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		readChan:  make(chan []byte, 10),
		writeChan: make(chan []byte, 10),
	}
}

func (m *mockTransport) Connect(ctx context.Context) error {
	if m.connectFunc != nil {
		return m.connectFunc(ctx)
	}
	return nil
}

func (m *mockTransport) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	close(m.readChan)
	close(m.writeChan)
	return nil
}

func (m *mockTransport) Send(data []byte) error {
	if m.sendFunc != nil {
		return m.sendFunc(data)
	}
	m.writeChan <- data
	return nil
}

func (m *mockTransport) Receive() ([]byte, error) {
	if m.receiveFunc != nil {
		return m.receiveFunc()
	}
	data, ok := <-m.readChan
	if !ok {
		return nil, nil
	}
	return data, nil
}

func (m *mockTransport) Reader() io.Reader {
	return nil
}

func (m *mockTransport) Writer() io.Writer {
	return nil
}

func TestClient_Initialize(t *testing.T) {
	transport := newMockTransport()
	client := NewClient(transport)

	// Simulate server response in background
	go func() {
		// Read initialize request
		reqData := <-transport.writeChan
		var req JSONRPCRequest
		json.Unmarshal(reqData, &req)

		// Send initialize response with matching ID
		response := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: json.RawMessage(`{
				"protocolVersion": "2024-11-05",
				"capabilities": {
					"tools": {}
				},
				"serverInfo": {
					"name": "test-server",
					"version": "1.0.0"
				}
			}`),
		}
		data, _ := json.Marshal(response)
		transport.readChan <- data

		// Read initialized notification
		<-transport.writeChan
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("failed to initialize: %v", err)
	}

	if client.serverInfo.Name == "" {
		t.Error("expected serverInfo to be set after initialization")
	}
}

func TestClient_ListTools(t *testing.T) {
	transport := newMockTransport()
	client := NewClient(transport)
	client.connected = true     // Skip initialization
	go client.handleResponses() // Start response handler

	// Simulate server response
	go func() {
		// Read tools/list request
		reqData := <-transport.writeChan
		var req JSONRPCRequest
		json.Unmarshal(reqData, &req)

		// Send tools response with matching ID
		response := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: json.RawMessage(`{
				"tools": [
					{
						"name": "read_file",
						"description": "Read a file",
						"inputSchema": {
							"type": "object",
							"properties": {
								"path": {"type": "string"}
							},
							"required": ["path"]
						}
					}
				]
			}`),
		}
		data, _ := json.Marshal(response)
		transport.readChan <- data
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}

	if tools[0].Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got '%s'", tools[0].Name)
	}
}

func TestClient_CallTool(t *testing.T) {
	transport := newMockTransport()
	client := NewClient(transport)
	client.connected = true
	go client.handleResponses() // Start response handler

	// Simulate server response
	go func() {
		// Read tools/call request
		reqData := <-transport.writeChan
		var req JSONRPCRequest
		json.Unmarshal(reqData, &req)

		// Send call result with matching ID
		response := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: json.RawMessage(`{
				"content": [
					{
						"type": "text",
						"text": "File contents here"
					}
				],
				"isError": false
			}`),
		}
		data, _ := json.Marshal(response)
		transport.readChan <- data
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	params := CallToolParams{
		Name: "read_file",
		Arguments: map[string]interface{}{
			"path": "/tmp/test.txt",
		},
	}

	result, err := client.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("failed to call tool: %v", err)
	}

	if result.IsError {
		t.Error("expected result.IsError to be false")
	}

	if len(result.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(result.Content))
	}

	if result.Content[0].Text != "File contents here" {
		t.Errorf("unexpected content: %s", result.Content[0].Text)
	}
}

func TestClient_Close(t *testing.T) {
	transport := newMockTransport()
	client := NewClient(transport)
	client.connected = true // Set as connected so Close() will call transport.Close()

	closed := false
	transport.closeFunc = func() error {
		closed = true
		return nil
	}

	err := client.Close()
	if err != nil {
		t.Fatalf("failed to close client: %v", err)
	}

	if !closed {
		t.Error("expected transport to be closed")
	}
}
