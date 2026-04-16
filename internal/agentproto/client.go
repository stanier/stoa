package agentproto

import (
	"context"

	"github.com/stanier/stoa/internal/chat"
)

// Client is the interface all agentd consumers implement.
// The TUI, Zulip bridge, and any future bridges all use this.
//
// The default implementation is WSClient in wsclient.go, which requires
// github.com/gorilla/websocket. Add it with:
//
//	GOPROXY=direct go get github.com/gorilla/websocket@v1.5.3
type Client interface {
	// CreateSession creates a new conversation session and returns its ID.
	CreateSession(ctx context.Context, cfg SessionConfig) (string, error)

	// ListSessions returns all active sessions on the server.
	ListSessions(ctx context.Context) ([]SessionInfo, error)

	// ClearSession resets conversation history for a session, keeping the system prompt.
	ClearSession(ctx context.Context, sessionID string) error

	// CompactSession triggers manual compaction on a session's conversation history.
	CompactSession(ctx context.Context, sessionID string) (CompactResponse, error)

	// GetTokens returns token counts for a session's current conversation history.
	GetTokens(ctx context.Context, sessionID string) (TokensResponse, error)

	// GetMessages returns the conversation history for a session, excluding system messages.
	GetMessages(ctx context.Context, sessionID string) ([]chat.Message, error)

	// RestoreMessages replaces a session's non-system conversation history with the given messages.
	RestoreMessages(ctx context.Context, sessionID string, messages []chat.Message) error

	// Connect attaches to a session and returns a bidirectional connection.
	Connect(ctx context.Context, sessionID string) (ClientConn, error)

	// Close releases resources held by the client.
	Close() error
}

// ClientConn is an active connection to a session.
type ClientConn interface {
	// Send sends a message to the server (SendMessage, PermResponse, Cancel).
	Send(msg ClientMessage) error

	// Events returns the channel of server→client events.
	// The channel is closed when the connection ends.
	Events() <-chan Event

	// Close closes the connection.
	Close() error
}
