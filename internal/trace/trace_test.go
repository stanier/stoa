package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stanier/stoa/internal/chat"
)

func TestNewTracer(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}
	defer tracer.Close()

	if !tracer.IsEnabled() {
		t.Error("Tracer should be enabled")
	}

	if tracer.FilePath() != tracePath {
		t.Errorf("Expected file path %s, got %s", tracePath, tracer.FilePath())
	}
}

func TestTracerDisabled(t *testing.T) {
	tracer, err := NewTracer("/tmp/unused.jsonl", false)
	if err != nil {
		t.Fatalf("Failed to create disabled tracer: %v", err)
	}
	defer tracer.Close()

	if tracer.IsEnabled() {
		t.Error("Tracer should be disabled")
	}

	// Should not error when logging to disabled tracer
	err = tracer.LogUserMessage("test")
	if err != nil {
		t.Errorf("Disabled tracer should not error on log: %v", err)
	}
}

func TestLogUserMessage(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	err = tracer.LogUserMessage("Hello, world!")
	if err != nil {
		t.Fatalf("Failed to log user message: %v", err)
	}

	tracer.Close()

	// Read and verify the trace file
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	var event TraceEvent
	err = json.Unmarshal(data, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal trace event: %v", err)
	}

	if event.Type != EventUserMessage {
		t.Errorf("Expected type %s, got %s", EventUserMessage, event.Type)
	}

	if event.Role != "user" {
		t.Errorf("Expected role 'user', got '%s'", event.Role)
	}

	if event.Content != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got '%s'", event.Content)
	}
}

func TestLogAssistantMessage(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	err = tracer.LogAssistantMessage("Hello!", "Let me think...")
	if err != nil {
		t.Fatalf("Failed to log assistant message: %v", err)
	}

	tracer.Close()

	// Read and verify
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	var event TraceEvent
	err = json.Unmarshal(data, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal trace event: %v", err)
	}

	if event.Type != EventAssistantMessage {
		t.Errorf("Expected type %s, got %s", EventAssistantMessage, event.Type)
	}

	if event.Content != "Hello!" {
		t.Errorf("Expected content 'Hello!', got '%s'", event.Content)
	}

	if event.Thinking != "Let me think..." {
		t.Errorf("Expected thinking 'Let me think...', got '%s'", event.Thinking)
	}
}

func TestLogToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	toolInput := map[string]interface{}{
		"operation": "add",
		"a":         5,
		"b":         3,
	}

	err = tracer.LogToolCall("calculator", "call_123", toolInput)
	if err != nil {
		t.Fatalf("Failed to log tool call: %v", err)
	}

	tracer.Close()

	// Read and verify
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	var event TraceEvent
	err = json.Unmarshal(data, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal trace event: %v", err)
	}

	if event.Type != EventToolCall {
		t.Errorf("Expected type %s, got %s", EventToolCall, event.Type)
	}

	if event.ToolName != "calculator" {
		t.Errorf("Expected tool name 'calculator', got '%s'", event.ToolName)
	}

	if event.ToolCallID != "call_123" {
		t.Errorf("Expected tool call ID 'call_123', got '%s'", event.ToolCallID)
	}

	if event.ToolInput["operation"] != "add" {
		t.Errorf("Expected operation 'add', got '%v'", event.ToolInput["operation"])
	}
}

func TestLogToolResult(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	err = tracer.LogToolResult("calculator", "call_123", "8")
	if err != nil {
		t.Fatalf("Failed to log tool result: %v", err)
	}

	tracer.Close()

	// Read and verify
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	var event TraceEvent
	err = json.Unmarshal(data, &event)
	if err != nil {
		t.Fatalf("Failed to unmarshal trace event: %v", err)
	}

	if event.Type != EventToolResult {
		t.Errorf("Expected type %s, got %s", EventToolResult, event.Type)
	}

	if event.ToolOutput != "8" {
		t.Errorf("Expected tool output '8', got '%s'", event.ToolOutput)
	}
}

func TestMultipleEvents(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_trace.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	// Log multiple events
	_ = tracer.LogUserMessage("What is 5 + 3?")
	_ = tracer.LogStreamStart()
	_ = tracer.LogToolCall("calculator", "call_1", map[string]interface{}{"op": "add", "a": 5, "b": 3})
	_ = tracer.LogToolResult("calculator", "call_1", "8")
	_ = tracer.LogAssistantMessage("The result is 8", "")
	_ = tracer.LogStreamComplete()

	tracer.Close()

	// Read all events
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("Failed to read trace file: %v", err)
	}

	// Count lines (each event is one line)
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}

	if lines != 6 {
		t.Errorf("Expected 6 events, got %d", lines)
	}
}
func TestLoadTrace(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "test_resume.jsonl")

	tracer, err := NewTracer(tracePath, true)
	if err != nil {
		t.Fatalf("Failed to create tracer: %v", err)
	}

	// Create a predictable trace
	_ = tracer.LogUserMessage("Hello")
	_ = tracer.LogAssistantMessage("Hi there", "Thinking...")
	_ = tracer.LogToolResult("calc", "call_1", "42")
	_ = tracer.LogSystemMessage("System check")
	tracer.Close()

	// Load it back
	messages, err := LoadTrace(tracePath)
	if err != nil {
		t.Fatalf("LoadTrace failed: %v", err)
	}

	if len(messages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(messages))
	}

	// Verify types and content
	if messages[0].Role != chat.RoleUser || messages[0].Content != "Hello" {
		t.Errorf("Message 0 mismatch: %+v", messages[0])
	}

	if messages[1].Role != chat.RoleAssistant || !strings.Contains(messages[1].Content, "Hi there") || messages[1].Thinking == "" {
		t.Errorf("Message 1 mismatch: %+v", messages[1])
	}

	if messages[2].Role != chat.RoleTool || messages[2].ToolName != "calc" || messages[2].Content != "42" {
		t.Errorf("Message 2 mismatch: %+v", messages[2])
	}

	if messages[3].Role != chat.RoleSystem || messages[3].Content != "System check" {
		t.Errorf("Message 3 mismatch: %+v", messages[3])
	}
}
