package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/chat"
)

// EventType represents the type of trace event
type EventType string

const (
	EventUserMessage      EventType = "user_message"
	EventAssistantMessage EventType = "assistant_message"
	EventToolCall         EventType = "tool_call"
	EventToolResult       EventType = "tool_result"
	EventSystemMessage    EventType = "system_message"
	EventStreamStart      EventType = "stream_start"
	EventStreamComplete   EventType = "stream_complete"
	EventError            EventType = "error"
)

// TraceEvent represents a single event in the conversation trace
type TraceEvent struct {
	Timestamp  time.Time              `json:"timestamp"`
	Type       EventType              `json:"type"`
	Role       string                 `json:"role,omitempty"`
	Content    string                 `json:"content,omitempty"`
	Thinking   string                 `json:"thinking,omitempty"`
	ToolName   string                 `json:"tool_name,omitempty"`
	ToolInput  map[string]interface{} `json:"tool_input,omitempty"`
	ToolOutput string                 `json:"tool_output,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Tracer handles writing trace events to a file
type Tracer struct {
	file     *os.File
	encoder  *json.Encoder
	mu       sync.Mutex
	enabled  bool
	filePath string
}

// NewTracer creates a new tracer that writes to the specified file
func NewTracer(filePath string, enabled bool) (*Tracer, error) {
	if !enabled {
		return &Tracer{enabled: false}, nil
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create trace directory: %w", err)
	}

	// Open file for appending
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file: %w", err)
	}

	return &Tracer{
		file:     file,
		encoder:  json.NewEncoder(file),
		enabled:  true,
		filePath: filePath,
	}, nil
}

// LogEvent writes a trace event to the file
func (t *Tracer) LogEvent(event TraceEvent) error {
	if t == nil || !t.enabled {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	event.Timestamp = time.Now()
	if err := t.encoder.Encode(event); err != nil {
		return fmt.Errorf("failed to write trace event: %w", err)
	}

	// Flush to ensure data is written
	if err := t.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync trace file: %w", err)
	}

	return nil
}

// LogUserMessage logs a user message
func (t *Tracer) LogUserMessage(content string) error {
	return t.LogEvent(TraceEvent{
		Type:    EventUserMessage,
		Role:    "user",
		Content: content,
	})
}

// LogAssistantMessage logs an assistant message
func (t *Tracer) LogAssistantMessage(content, thinking string) error {
	return t.LogEvent(TraceEvent{
		Type:     EventAssistantMessage,
		Role:     "assistant",
		Content:  content,
		Thinking: thinking,
	})
}

// LogToolCall logs a tool call
func (t *Tracer) LogToolCall(toolName, toolCallID string, input map[string]interface{}) error {
	return t.LogEvent(TraceEvent{
		Type:       EventToolCall,
		ToolName:   toolName,
		ToolCallID: toolCallID,
		ToolInput:  input,
	})
}

// LogToolResult logs a tool result
func (t *Tracer) LogToolResult(toolName, toolCallID, output string) error {
	return t.LogEvent(TraceEvent{
		Type:       EventToolResult,
		ToolName:   toolName,
		ToolCallID: toolCallID,
		ToolOutput: output,
	})
}

// LogSystemMessage logs a system message
func (t *Tracer) LogSystemMessage(content string) error {
	return t.LogEvent(TraceEvent{
		Type:    EventSystemMessage,
		Role:    "system",
		Content: content,
	})
}

// LogStreamStart logs the start of a streaming response
func (t *Tracer) LogStreamStart() error {
	return t.LogEvent(TraceEvent{
		Type: EventStreamStart,
	})
}

// LogStreamComplete logs the completion of a streaming response
func (t *Tracer) LogStreamComplete() error {
	return t.LogEvent(TraceEvent{
		Type: EventStreamComplete,
	})
}

// LogError logs an error event
func (t *Tracer) LogError(err error) error {
	return t.LogEvent(TraceEvent{
		Type:  EventError,
		Error: err.Error(),
	})
}

// LogChatMessage logs a chat.Message
func (t *Tracer) LogChatMessage(msg chat.Message) error {
	switch msg.Role {
	case chat.RoleUser:
		return t.LogUserMessage(msg.Content)
	case chat.RoleAssistant:
		return t.LogAssistantMessage(msg.Content, "")
	case chat.RoleSystem:
		return t.LogSystemMessage(msg.Content)
	default:
		return t.LogEvent(TraceEvent{
			Type:    EventType(msg.Role),
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}
}

// Close closes the trace file
func (t *Tracer) Close() error {
	if !t.enabled || t.file == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.file.Close()
}

// FilePath returns the path to the trace file
func (t *Tracer) FilePath() string {
	return t.filePath
}

// IsEnabled returns whether tracing is enabled
func (t *Tracer) IsEnabled() bool {
	return t.enabled
}

// LoadTrace loads a conversation trace from a file and returns a slice of chat.Message
func LoadTrace(filePath string) ([]chat.Message, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open trace file: %w", err)
	}
	defer file.Close()

	var messages []chat.Message
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event TraceEvent
		if err := json.Unmarshal(line, &event); err != nil {
			// Skip malformed lines instead of failing entirely
			continue
		}

		var msg chat.Message
		switch event.Type {
		case EventUserMessage:
			msg = chat.NewUserMessage(event.Content)
		case EventAssistantMessage:
			msg = chat.NewAssistantMessage(event.Content)
			msg.Thinking = event.Thinking
		case EventSystemMessage:
			msg = chat.NewSystemMessage(event.Content)
		case EventToolResult:
			msg = chat.NewToolResultMessage(event.ToolName, event.ToolCallID, event.ToolOutput, true)
		case EventToolCall, EventStreamStart, EventStreamComplete, EventError:
			// These are events but not necessarily distinct messages in the history
			continue
		default:
			// Handle other roles (note, summary) which use the role as Type
			role := chat.Role(event.Type)
			msg = chat.NewMessage(role, event.Content)
		}

		msg.Timestamp = event.Timestamp
		if msg.Role != "" {
			messages = append(messages, msg)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading trace file: %w", err)
	}

	return messages, nil
}
