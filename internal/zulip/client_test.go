package zulip

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendMessage_FormEncoded(t *testing.T) {
	// Create a test server to verify form encoding
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/x-www-form-urlencoded" {
			t.Errorf("Expected Content-Type 'application/x-www-form-urlencoded', got '%s'", contentType)
		}

		// Parse form data
		if err := r.ParseForm(); err != nil {
			t.Fatalf("Failed to parse form: %v", err)
		}

		// Verify form fields
		if r.FormValue("type") != "stream" {
			t.Errorf("Expected type 'stream', got '%s'", r.FormValue("type"))
		}
		if r.FormValue("to") != "general" {
			t.Errorf("Expected to 'general', got '%s'", r.FormValue("to"))
		}
		if r.FormValue("content") != "Hello, World!" {
			t.Errorf("Expected content 'Hello, World!', got '%s'", r.FormValue("content"))
		}
		if r.FormValue("topic") != "test topic" {
			t.Errorf("Expected topic 'test topic', got '%s'", r.FormValue("topic"))
		}

		// Send success response
		resp := SendMessageResponse{
			ID:     12345,
			Result: "success",
			Msg:    "",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client pointing to test server
	client := &Client{
		baseURL: server.URL,
		email:   "test@example.com",
		apiKey:  "test-key",
		client:  &http.Client{},
	}

	// Send a message
	msgID, err := client.SendMessage(context.Background(), "stream", "general", "Hello, World!", "test topic")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if msgID != 12345 {
		t.Errorf("Expected message ID 12345, got %d", msgID)
	}
}

func TestSendMessage_MissingContent(t *testing.T) {
	// Create a test server that simulates Zulip's error response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send error response for missing content
		w.WriteHeader(http.StatusBadRequest)
		resp := map[string]interface{}{
			"result": "error",
			"msg":    "Missing 'content' argument",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
		email:   "test@example.com",
		apiKey:  "test-key",
		client:  &http.Client{},
	}

	// Try to send a message with empty content
	_, err := client.SendMessage(context.Background(), "stream", "general", "", "test topic")
	if err == nil {
		t.Error("Expected error for missing content, got nil")
	}

	if err != nil && err.Error() != "API error (HTTP 400): Missing 'content' argument" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestRegisterQueue_JSON(t *testing.T) {
	// Verify that RegisterQueue still uses JSON (not form-encoded)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
		}

		// Read and verify JSON body
		body, _ := io.ReadAll(r.Body)
		var req RegisterQueueRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("Failed to unmarshal JSON: %v", err)
		}

		// Send success response
		resp := RegisterQueueResponse{
			QueueID:     "test-queue-123",
			LastEventID: 0,
			Result:      "success",
			Msg:         "",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
		email:   "test@example.com",
		apiKey:  "test-key",
		client:  &http.Client{},
	}

	queueInfo, err := client.RegisterQueue(context.Background(), []string{"message"})
	if err != nil {
		t.Fatalf("RegisterQueue failed: %v", err)
	}

	if queueInfo.QueueID != "test-queue-123" {
		t.Errorf("Expected queue ID 'test-queue-123', got '%s'", queueInfo.QueueID)
	}
}
