package zulip

import (
	"encoding/json"
	"testing"
)

func TestDisplayRecipient_UnmarshalJSON_Stream(t *testing.T) {
	// Test stream message (display_recipient is a string)
	jsonData := `{
		"id": 123,
		"content": "test message",
		"sender_id": 456,
		"sender_email": "user@example.com",
		"sender_full_name": "Test User",
		"type": "stream",
		"display_recipient": "general",
		"subject": "test topic",
		"timestamp": 1234567890
	}`

	var msg Message
	err := json.Unmarshal([]byte(jsonData), &msg)
	if err != nil {
		t.Fatalf("Failed to unmarshal stream message: %v", err)
	}

	if msg.DisplayRecipient.String() != "general" {
		t.Errorf("Expected display_recipient 'general', got '%s'", msg.DisplayRecipient.String())
	}

	if msg.DisplayRecipient.Stream != "general" {
		t.Errorf("Expected Stream field to be 'general', got '%s'", msg.DisplayRecipient.Stream)
	}

	if len(msg.DisplayRecipient.Recipients) != 0 {
		t.Errorf("Expected Recipients to be empty for stream message")
	}
}

func TestDisplayRecipient_UnmarshalJSON_Private(t *testing.T) {
	// Test private message (display_recipient is an array)
	jsonData := `{
		"id": 123,
		"content": "test message",
		"sender_id": 456,
		"sender_email": "user@example.com",
		"sender_full_name": "Test User",
		"type": "private",
		"display_recipient": [
			{
				"email": "recipient@example.com",
				"full_name": "Recipient User",
				"id": 789
			}
		],
		"subject": "",
		"timestamp": 1234567890
	}`

	var msg Message
	err := json.Unmarshal([]byte(jsonData), &msg)
	if err != nil {
		t.Fatalf("Failed to unmarshal private message: %v", err)
	}

	if msg.DisplayRecipient.String() != "recipient@example.com" {
		t.Errorf("Expected display_recipient 'recipient@example.com', got '%s'", msg.DisplayRecipient.String())
	}

	if msg.DisplayRecipient.Stream != "" {
		t.Errorf("Expected Stream field to be empty for private message")
	}

	if len(msg.DisplayRecipient.Recipients) != 1 {
		t.Fatalf("Expected 1 recipient, got %d", len(msg.DisplayRecipient.Recipients))
	}

	if msg.DisplayRecipient.Recipients[0].Email != "recipient@example.com" {
		t.Errorf("Expected recipient email 'recipient@example.com', got '%s'", msg.DisplayRecipient.Recipients[0].Email)
	}

	if msg.DisplayRecipient.Recipients[0].FullName != "Recipient User" {
		t.Errorf("Expected recipient name 'Recipient User', got '%s'", msg.DisplayRecipient.Recipients[0].FullName)
	}
}

func TestDisplayRecipient_UnmarshalJSON_MultipleRecipients(t *testing.T) {
	// Test group private message (multiple recipients)
	jsonData := `{
		"id": 123,
		"content": "test message",
		"sender_id": 456,
		"sender_email": "user@example.com",
		"sender_full_name": "Test User",
		"type": "private",
		"display_recipient": [
			{
				"email": "recipient1@example.com",
				"full_name": "Recipient One",
				"id": 789
			},
			{
				"email": "recipient2@example.com",
				"full_name": "Recipient Two",
				"id": 790
			}
		],
		"subject": "",
		"timestamp": 1234567890
	}`

	var msg Message
	err := json.Unmarshal([]byte(jsonData), &msg)
	if err != nil {
		t.Fatalf("Failed to unmarshal group message: %v", err)
	}

	if len(msg.DisplayRecipient.Recipients) != 2 {
		t.Fatalf("Expected 2 recipients, got %d", len(msg.DisplayRecipient.Recipients))
	}

	// String() should return first recipient's email
	if msg.DisplayRecipient.String() != "recipient1@example.com" {
		t.Errorf("Expected display_recipient 'recipient1@example.com', got '%s'", msg.DisplayRecipient.String())
	}
}
