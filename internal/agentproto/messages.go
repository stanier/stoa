package agentproto

// MessageType identifies the client→server message type.
type MessageType string

const (
	MessageTypeSendMessage  MessageType = "send_message"
	MessageTypePermResponse MessageType = "perm_response"
	MessageTypeCancel       MessageType = "cancel"
)

// ClientMessage is the client→server message over the WebSocket connection.
// Exactly one of the payload fields is non-nil.
type ClientMessage struct {
	Type         MessageType          `json:"type"`
	SendMessage  *SendMessagePayload  `json:"send_message,omitempty"`
	PermResponse *PermResponsePayload `json:"perm_response,omitempty"`
}

// SendMessagePayload carries a user message to be processed by the session.
type SendMessagePayload struct {
	Content string `json:"content"`
}

// PermResponsePayload carries the client's approval/denial of a permission request.
type PermResponsePayload struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}
