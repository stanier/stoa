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

// AttachmentRef is a reference to an uploaded file sent alongside a message.
// Exactly one of AttachmentID (S3 mode) or Path (local filesystem fallback) is set.
type AttachmentRef struct {
	Name         string `json:"name"`
	MimeType     string `json:"mime_type"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Path         string `json:"path,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// ImagePart carries a resolved image for direct inclusion in a multimodal message.
// URL is a data URI (data:image/...;base64,...) or an https:// URL.
type ImagePart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// SendMessagePayload carries a user message to be processed by the session.
// Attachments holds raw upload refs from the browser; ImageParts holds resolved
// image content injected by webd before the frame is forwarded to agentd.
type SendMessagePayload struct {
	Content     string          `json:"content"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	ImageParts  []ImagePart     `json:"image_parts,omitempty"`
}

// PermResponsePayload carries the client's approval/denial of a permission request.
type PermResponsePayload struct {
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}
