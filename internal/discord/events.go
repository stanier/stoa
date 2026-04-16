// Package discord provides a thin wrapper around the Discord Gateway API
// used by discord-bridge to receive messages and send responses.
package discord

// ReactionEvent represents a reaction added to a Discord message.
type ReactionEvent struct {
	UserID    string
	ChannelID string
	MessageID string
	Emoji     string // Unicode emoji or name (e.g. "✅" or "white_check_mark")
}

// MessageEvent represents an incoming Discord message.
type MessageEvent struct {
	ChannelID   string
	UserID      string
	Username    string
	Text        string
	MessageID   string
	ChannelType int // discordgo.ChannelType value
	IsBot       bool

	// Thread/guild fields.
	// GuildID is non-empty for messages in guild (server) channels.
	GuildID string
	// ThreadID is non-empty when the message was sent inside a thread.
	ThreadID string
	// IsThread is true when the message arrived in a thread channel.
	IsThread bool
	// IsGuildChannel is true when the message is in a guild text channel (not a DM, not a thread).
	IsGuildChannel bool
}

// ConnectionStatus represents a connect/disconnect event.
type ConnectionStatus struct {
	Connected bool
	Error     error
}
