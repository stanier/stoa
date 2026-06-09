package inferencegateway

import (
	"encoding/json"
	"testing"

	"github.com/stanier/stoa/internal/chat"
)

func TestParseOpenAIMessages_ImageURL(t *testing.T) {
	// A user message with both text and image_url content parts.
	msgs := []OpenAIMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image_url","image_url":{"url":"https://example.com/photo.jpg","detail":"high"}},
				{"type":"text","text":"What is in this image?"}
			]`),
		},
	}

	result, err := ParseOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg := result[0]
	if msg.Role != chat.RoleUser {
		t.Errorf("expected role user, got %q", msg.Role)
	}
	if msg.Content != "What is in this image?" {
		t.Errorf("unexpected content %q", msg.Content)
	}
	if len(msg.ImageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(msg.ImageParts))
	}
	if msg.ImageParts[0].URL != "https://example.com/photo.jpg" {
		t.Errorf("unexpected image URL %q", msg.ImageParts[0].URL)
	}
	if msg.ImageParts[0].Detail != "high" {
		t.Errorf("unexpected detail %q", msg.ImageParts[0].Detail)
	}
}

func TestParseOpenAIMessages_ImageDataURI(t *testing.T) {
	// A user message with a data: URI image (base64 inline).
	msgs := []OpenAIMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}},
				{"type":"text","text":"describe this"}
			]`),
		},
	}

	result, err := ParseOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := result[0]
	if len(msg.ImageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(msg.ImageParts))
	}
	if msg.ImageParts[0].URL != "data:image/png;base64,iVBORw0KGgo=" {
		t.Errorf("unexpected data URI %q", msg.ImageParts[0].URL)
	}
}

func TestParseOpenAIMessages_ImageOnly(t *testing.T) {
	// A user message that is only an image (no text).
	msgs := []OpenAIMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}
			]`),
		},
	}

	result, err := ParseOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := result[0]
	if msg.Content != "" {
		t.Errorf("expected empty content, got %q", msg.Content)
	}
	if len(msg.ImageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(msg.ImageParts))
	}
}

func TestParseOpenAIMessages_PlainText(t *testing.T) {
	// Existing plain-string messages must still work.
	msgs := []OpenAIMessage{
		{Role: "user", Content: json.RawMessage(`"hello"`)},
	}
	result, err := ParseOpenAIMessages(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].Content != "hello" {
		t.Errorf("unexpected content %q", result[0].Content)
	}
	if len(result[0].ImageParts) != 0 {
		t.Errorf("expected no image parts for plain text message")
	}
}

func TestParseAnthropicMessages_Base64Image(t *testing.T) {
	// Anthropic-format user message with a base64 image block + text.
	msgs := []AnthropicMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}},
				{"type":"text","text":"What do you see?"}
			]`),
		},
	}

	result, err := ParseAnthropicMessages(msgs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	msg := result[0]
	if msg.Role != chat.RoleUser {
		t.Errorf("expected role user, got %q", msg.Role)
	}
	if msg.Content != "What do you see?" {
		t.Errorf("unexpected content %q", msg.Content)
	}
	if len(msg.ImageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(msg.ImageParts))
	}
	if msg.ImageParts[0].URL != "data:image/png;base64,iVBORw0KGgo=" {
		t.Errorf("unexpected image URL %q", msg.ImageParts[0].URL)
	}
}

func TestParseAnthropicMessages_URLImage(t *testing.T) {
	// Anthropic-format user message with a URL image source.
	msgs := []AnthropicMessage{
		{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image","source":{"type":"url","url":"https://example.com/photo.jpg"}},
				{"type":"text","text":"describe it"}
			]`),
		},
	}

	result, err := ParseAnthropicMessages(msgs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := result[0]
	if len(msg.ImageParts) != 1 {
		t.Fatalf("expected 1 image part, got %d", len(msg.ImageParts))
	}
	if msg.ImageParts[0].URL != "https://example.com/photo.jpg" {
		t.Errorf("unexpected image URL %q", msg.ImageParts[0].URL)
	}
	if msg.Content != "describe it" {
		t.Errorf("unexpected content %q", msg.Content)
	}
}

func TestParseOpenAIMessages_UnsupportedPartType(t *testing.T) {
	// input_audio must still return an error.
	msgs := []OpenAIMessage{
		{
			Role:    "user",
			Content: json.RawMessage(`[{"type":"input_audio"}]`),
		},
	}
	_, err := ParseOpenAIMessages(msgs)
	if err == nil {
		t.Fatal("expected error for unsupported part type, got nil")
	}
}
