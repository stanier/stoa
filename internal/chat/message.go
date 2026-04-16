package chat

import "time"

// Role represents the sender of a message
type Role string

// ImagePart carries a single vision content part from a multimodal message.
// URL may be an https:// URL or a data: URI (base64-encoded image).
type ImagePart struct {
	URL    string // https:// URL or data: URI
	Detail string // "" | "auto" | "low" | "high"
}

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleSummary   Role = "summary"
	RoleTool      Role = "tool"
	RoleNote      Role = "note"
)

// Message represents a single chat message
type Message struct {
	Role      Role
	Content   string
	Thinking  string    `json:"thinking,omitempty"`
	Timestamp time.Time
	// Tool-specific metadata
	ToolName    string // Name of the tool (for RoleTool messages)
	ToolUseID   string // Tool use ID (for RoleTool messages)
	ToolSuccess bool   // Whether tool execution succeeded
	// Vision content parts (user messages only). When non-nil the message
	// carries multimodal content; Content holds any accompanying text.
	ImageParts []ImagePart `json:"image_parts,omitempty"`
}

func NewMessage(role Role, content string) Message {
	return Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}
}

func NewUserMessage(content string) Message {
	return NewMessage(RoleUser, content)
}

func NewAssistantMessage(content string) Message {
	return NewMessage(RoleAssistant, content)
}

func NewSystemMessage(content string) Message {
	return NewMessage(RoleSystem, content)
}

func NewSummaryMessage(content string) Message {
	return NewMessage(RoleSummary, content)
}

func NewNoteMessage(content string) Message {
	return NewMessage(RoleNote, content)
}

func NewToolResultMessage(toolName, toolUseID, content string, success bool) Message {
	msg := NewMessage(RoleTool, content)
	msg.ToolName = toolName
	msg.ToolUseID = toolUseID
	msg.ToolSuccess = success
	return msg
}
