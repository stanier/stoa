package zulip

import (
	"encoding/json"
	"time"
)

// QueueInfo represents a Zulip event queue
type QueueInfo struct {
	QueueID     string `json:"queue_id"`
	LastEventID int    `json:"last_event_id"`
}

// Event represents a Zulip event from the event queue
type Event struct {
	ID   int      `json:"id"`
	Type string   `json:"type"`
	Op   string   `json:"op,omitempty"` // "add", "remove", etc.
	Msg  *Message `json:"message,omitempty"`

	// Reaction fields
	UserID       int    `json:"user_id,omitempty"`
	MessageID    int    `json:"message_id,omitempty"`
	EmojiName    string `json:"emoji_name,omitempty"`
	EmojiCode    string `json:"emoji_code,omitempty"`
	ReactionType string `json:"reaction_type,omitempty"`
}

// Recipient represents a recipient in a private message
type Recipient struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	ID       int    `json:"id"`
}

// DisplayRecipient can be either a string (stream) or array (private message)
type DisplayRecipient struct {
	Stream     string      // For stream messages
	Recipients []Recipient // For private messages
}

// UnmarshalJSON implements custom unmarshaling for DisplayRecipient
func (dr *DisplayRecipient) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first (stream message)
	var stream string
	if err := json.Unmarshal(data, &stream); err == nil {
		dr.Stream = stream
		dr.Recipients = nil
		return nil
	}

	// Try to unmarshal as array (private message)
	var recipients []Recipient
	if err := json.Unmarshal(data, &recipients); err == nil {
		dr.Stream = ""
		dr.Recipients = recipients
		return nil
	}

	return nil
}

// String returns a string representation of the recipient
func (dr *DisplayRecipient) String() string {
	if dr.Stream != "" {
		return dr.Stream
	}
	if len(dr.Recipients) > 0 {
		return dr.Recipients[0].Email
	}
	return ""
}

// MessageAttachment represents a file attached to a Zulip message.
type MessageAttachment struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Size   int    `json:"size"`    // bytes
	PathID string `json:"path_id"` // used to build the download URL
}

// Message represents a Zulip message
type Message struct {
	ID               int                 `json:"id"`
	Content          string              `json:"content"`
	SenderID         int                 `json:"sender_id"`
	SenderEmail      string              `json:"sender_email"`
	SenderFullName   string              `json:"sender_full_name"`
	Type             string              `json:"type"` // "stream"/"channel" (Zulip 9+) or "private"
	DisplayRecipient DisplayRecipient    `json:"display_recipient"`
	Subject          string              `json:"subject"`
	Timestamp        int64               `json:"timestamp"`
	Attachments      []MessageAttachment `json:"attachments,omitempty"`
}

// SendMessageRequest represents a request to send a message
type SendMessageRequest struct {
	Type    string `json:"type"`
	To      string `json:"to"`
	Content string `json:"content"`
	Topic   string `json:"topic,omitempty"`
}

// SendMessageResponse represents the response when sending a message
type SendMessageResponse struct {
	ID     int    `json:"id"`
	Result string `json:"result"`
	Msg    string `json:"msg"`
}

// AddReactionRequest represents a request to add a reaction
type AddReactionRequest struct {
	EmojiName    string `json:"emoji_name"`
	ReactionType string `json:"reaction_type"`
}

// RegisterQueueRequest represents a request to register an event queue
type RegisterQueueRequest struct {
	EventTypes []string        `json:"event_types"`
	Narrow     [][]interface{} `json:"narrow,omitempty"`
}

// RegisterQueueResponse represents the response when registering a queue
type RegisterQueueResponse struct {
	QueueID     string `json:"queue_id"`
	LastEventID int    `json:"last_event_id"`
	Result      string `json:"result"`
	Msg         string `json:"msg"`
}

// GetEventsResponse represents the response from polling events
type GetEventsResponse struct {
	Events []Event `json:"events"`
	Result string  `json:"result"`
	Msg    string  `json:"msg"`
}

// DeleteQueueRequest represents a request to delete an event queue
type DeleteQueueRequest struct {
	QueueID string `json:"queue_id"`
}

// Error represents an API error response
type Error struct {
	Result string `json:"result"`
	Msg    string `json:"msg"`
	Code   string `json:"code,omitempty"`
}

// ReactionInfo tracks reactions on a message during processing
type ReactionInfo struct {
	MessageID int
	UserID    int
	EmojiName string
	AddedAt   time.Time
}
