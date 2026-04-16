package transport

import (
	"context"
	"io"
)

// Transport handles communication with an MCP server
type Transport interface {
	// Connect establishes connection to the server
	Connect(ctx context.Context) error

	// Close closes the connection
	Close() error

	// Send sends a message to the server
	Send(data []byte) error

	// Receive receives a message from the server
	Receive() ([]byte, error)

	// Reader returns the reader for streaming responses
	Reader() io.Reader

	// Writer returns the writer for sending requests
	Writer() io.Writer
}
