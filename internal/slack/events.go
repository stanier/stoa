// Package slack provides a thin wrapper around the Slack Socket Mode API
// used by slack-bridge to receive messages and send responses.
package slack

// ReactionEvent represents a reaction added to a Slack message.
type ReactionEvent struct {
	// UserID is the Slack user ID who added the reaction.
	UserID string
	// ChannelID is the channel where the reacted-on message lives.
	ChannelID string
	// MessageTS is the timestamp (ID) of the message that was reacted to.
	MessageTS string
	// Emoji is the reaction emoji name (e.g. "white_check_mark", "x").
	Emoji string
}

// MessageEvent represents an incoming Slack message.
type MessageEvent struct {
	// ChannelID is the Slack channel ID.
	ChannelID string
	// UserID is the Slack user ID of the sender.
	UserID string
	// Text is the message text content.
	Text string
	// Timestamp is the Slack message timestamp (also serves as message ID).
	Timestamp string
	// ThreadTimestamp is non-empty when the message is in a thread.
	ThreadTimestamp string
	// ChannelType is "im" for DMs, "channel" for public channels, etc.
	ChannelType string
	// BotID is non-empty when the message was sent by a bot.
	BotID string
}

// ConnectionStatus represents a connect/disconnect event from the Socket Mode client.
type ConnectionStatus struct {
	Connected bool
	Error     error
}

// BlockActionEvent represents a block_actions interaction (button click) from Slack.
type BlockActionEvent struct {
	// UserID is the Slack user ID who clicked the button.
	UserID string
	// ChannelID is the channel where the interactive message lives.
	ChannelID string
	// MessageTS is the timestamp of the interactive message.
	MessageTS string
	// ActionID is the action_id of the button that was clicked.
	ActionID string
	// Value is the value associated with the button.
	Value string
}
