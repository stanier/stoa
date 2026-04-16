package channels

import "context"

// Channel represents a communication channel (Zulip, Slack, Discord, etc.)
type Channel interface {
	// Start initializes and starts the channel connection
	Start(ctx context.Context) error

	// Stop gracefully shuts down the channel
	Stop(ctx context.Context) error

	// IsConnected returns whether the channel is currently connected
	IsConnected() bool

	// Messages returns a channel for receiving incoming messages
	Messages() <-chan Message

	// SendResponse sends a response back to the channel
	SendResponse(originalMsg Message, response string, err error) error

	// AddReaction adds a reaction emoji to a message (if supported by the channel)
	AddReaction(originalMsg Message, emoji string) error

	// Name returns the channel type name (e.g., "zulip", "slack")
	Name() string
}

// Attachment represents a file attached to a channel message.
type Attachment struct {
	Name   string // original filename
	PathID string // source-specific identifier for downloading
	Size   int    // size in bytes
}

// Message represents a message from any channel
type Message struct {
	// Universal fields
	ID          string // Unique message ID
	Content     string // Message text content
	SenderID    string // Sender unique ID
	SenderName  string // Sender display name
	SenderEmail string // Sender email address (populated where available)

	// Context fields
	Type      string // Message type: "direct", "channel", "thread", etc.
	Recipient string // Channel/stream/room name or user ID
	Topic     string // Thread topic (for threaded channels like Zulip)

	// Channel-specific data
	Raw         interface{} // Original message object for channel-specific operations
	Attachments []Attachment // attached files, if any
}

// StatusUpdate represents a connection status change
type StatusUpdate struct {
	ChannelName string
	Connected   bool
	Error       error
}
