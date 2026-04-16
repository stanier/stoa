// Package matrix provides a thin wrapper around the Matrix Client-Server API
// used by matrix-bridge to receive messages and send responses.
package matrix

// ReactionEvent represents a reaction added to a Matrix event.
type ReactionEvent struct {
	RoomID      string
	SenderID    string
	TargetEvent string // event ID of the message being reacted to
	Emoji       string // Unicode emoji key (e.g. "✅")
}

// MessageEvent represents an incoming Matrix message.
type MessageEvent struct {
	RoomID    string
	EventID   string
	SenderID  string // full Matrix user ID: @user:server.com
	Username  string // display name or localpart
	Text      string
	IsDM      bool

	// Thread support: set when the message is part of an m.thread relation.
	IsThreaded   bool   // true if this message has an m.thread relation
	ThreadRootID string // event ID of the thread root event
}

// ConnectionStatus represents a connect/disconnect event.
type ConnectionStatus struct {
	Connected bool
	Error     error
}
