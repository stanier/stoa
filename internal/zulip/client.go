package zulip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client represents a Zulip API client
type Client struct {
	baseURL     string
	siteURL     string
	email       string
	apiKey      string
	queueID     string
	lastEventID int
	client      *http.Client
}

// NewClient creates a new Zulip API client
func NewClient(siteURL, email, apiKey string) *Client {
	trimmed := strings.TrimRight(siteURL, "/")
	return &Client{
		baseURL: fmt.Sprintf("%s/api/v1", trimmed),
		siteURL: trimmed,
		email:   email,
		apiKey:  apiKey,
		client:  &http.Client{},
	}
}

// DownloadFile downloads a Zulip user upload by path_id and returns its raw bytes.
// The download URL is {siteURL}/user_uploads/{pathID} with Basic Auth.
func (c *Client) DownloadFile(ctx context.Context, pathID string) ([]byte, error) {
	downloadURL := c.siteURL + "/user_uploads/" + pathID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.email, c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// RegisterQueue registers an event queue with the Zulip server
func (c *Client) RegisterQueue(ctx context.Context, eventTypes []string) (*QueueInfo, error) {
	req := RegisterQueueRequest{
		EventTypes: eventTypes,
	}

	var resp RegisterQueueResponse
	if err := c.post(ctx, "/register", req, &resp); err != nil {
		return nil, err
	}

	if resp.Result != "success" {
		return nil, fmt.Errorf("failed to register queue: %s", resp.Msg)
	}

	c.queueID = resp.QueueID
	c.lastEventID = resp.LastEventID

	return &QueueInfo{
		QueueID:     resp.QueueID,
		LastEventID: resp.LastEventID,
	}, nil
}

// GetEvents polls for new events from the queue
func (c *Client) GetEvents(ctx context.Context, queueID string, lastEventID int) ([]Event, error) {
	params := url.Values{}
	params.Set("queue_id", queueID)
	params.Set("last_event_id", fmt.Sprintf("%d", lastEventID))

	var resp GetEventsResponse
	if err := c.getWithParams(ctx, "/events", params, &resp); err != nil {
		return nil, err
	}

	if resp.Result != "success" {
		return nil, fmt.Errorf("failed to get events: %s", resp.Msg)
	}

	return resp.Events, nil
}

// SendMessage sends a message to a Zulip stream or user
func (c *Client) SendMessage(ctx context.Context, msgType, to, content, topic string) (int, error) {
	// Zulip's /messages endpoint expects form-encoded data, not JSON
	params := url.Values{}
	params.Set("type", msgType)
	params.Set("to", to)
	params.Set("content", content)
	if topic != "" {
		params.Set("topic", topic)
	}

	var resp SendMessageResponse
	if err := c.postForm(ctx, "/messages", params, &resp); err != nil {
		return 0, err
	}

	if resp.Result != "success" {
		return 0, fmt.Errorf("failed to send message: %s", resp.Msg)
	}

	return resp.ID, nil
}

// AddReaction adds a reaction to a message. It tries unicode_emoji first,
// then falls back to zulip_extra_emoji for Zulip-specific emoji like :check:.
func (c *Client) AddReaction(ctx context.Context, messageID int, emoji string) error {
	for _, reactionType := range []string{"unicode_emoji", "zulip_extra_emoji"} {
		err := c.addReactionWithType(ctx, messageID, emoji, reactionType)
		if err == nil {
			return nil
		}
		// Only fall through to the next type on a semantic API error — not on
		// network failures or rate limits, which should propagate immediately.
		if isTransientError(err) {
			return err
		}
	}
	return fmt.Errorf("add reaction %q: failed for all reaction types", emoji)
}

// addReactionWithType sends a single reaction-add request with the given reaction_type.
func (c *Client) addReactionWithType(ctx context.Context, messageID int, emoji, reactionType string) error {
	data := url.Values{}
	data.Set("emoji_name", emoji)
	data.Set("reaction_type", reactionType)

	path := fmt.Sprintf("/messages/%d/reactions", messageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.email, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errResp Error
		if jsonErr := json.Unmarshal(respBody, &errResp); jsonErr == nil && errResp.Msg != "" {
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, errResp.Msg)
		}
		return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Zulip can return HTTP 200 with {"result":"error",...} for semantic errors
	// such as wrong reaction_type for the given emoji. Without this check the
	// error is silently swallowed.
	var result Error
	if jsonErr := json.Unmarshal(respBody, &result); jsonErr == nil && result.Result == "error" {
		return fmt.Errorf("API error: %s", result.Msg)
	}

	return nil
}

// isTransientError reports whether err is a network or rate-limit failure that
// should not trigger a reaction_type fallback.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "request failed:") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "HTTP 429")
}

// RemoveReaction removes a reaction from a message
func (c *Client) RemoveReaction(ctx context.Context, messageID int, emoji string) error {
	params := url.Values{}
	params.Set("emoji_name", emoji)
	params.Set("reaction_type", "unicode_emoji")

	path := fmt.Sprintf("/messages/%d/reactions?%s", messageID, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.email, c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		var errResp Error
		if err := json.Unmarshal(body, &errResp); err == nil && errResp.Result != "success" {
			return fmt.Errorf("failed to remove reaction: %s", errResp.Msg)
		}
		return fmt.Errorf("failed to remove reaction: status %d", resp.StatusCode)
	}

	return nil
}

// GetOwnUserID fetches the authenticated bot's own user ID via GET /users/me
func (c *Client) GetOwnUserID(ctx context.Context) (int, error) {
	id, _, err := c.GetOwnProfile(ctx)
	return id, err
}

// GetOwnProfile fetches the bot's user ID and full name via GET /users/me.
func (c *Client) GetOwnProfile(ctx context.Context) (int, string, error) {
	var result struct {
		UserID   int    `json:"user_id"`
		FullName string `json:"full_name"`
		Result   string `json:"result"`
		Msg      string `json:"msg"`
	}
	if err := c.getWithParams(ctx, "/users/me", nil, &result); err != nil {
		return 0, "", err
	}
	if result.Result != "success" {
		return 0, "", fmt.Errorf("failed to get own profile: %s", result.Msg)
	}
	return result.UserID, result.FullName, nil
}

// DeleteQueue deletes an event queue
func (c *Client) DeleteQueue(ctx context.Context, queueID string) error {
	req := DeleteQueueRequest{
		QueueID: queueID,
	}

	var result map[string]interface{}
	if err := c.delete(ctx, "/events", req, &result); err != nil {
		return err
	}

	return nil
}

// Helper methods

// postForm sends a POST request with form-encoded data to the Zulip API
func (c *Client) postForm(ctx context.Context, path string, formData url.Values, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewBufferString(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.email, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Try to parse error response
		var errResp Error
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Msg != "" {
			if resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("rate limited: %s", errResp.Msg)
			}
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, errResp.Msg)
		}
		// Fallback if we can't parse error response
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("rate limited by Zulip API")
		}
		return fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response (HTTP %d): %w", resp.StatusCode, err)
	}

	return nil
}

// post sends a POST request to the Zulip API
func (c *Client) post(ctx context.Context, path string, body interface{}, result interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.email, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Try to parse error response
		var errResp Error
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Msg != "" {
			if resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("rate limited: %s", errResp.Msg)
			}
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, errResp.Msg)
		}
		// Fallback if we can't parse error response
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("rate limited by Zulip API")
		}
		return fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response (HTTP %d): %w", resp.StatusCode, err)
	}

	return nil
}

// getWithParams sends a GET request with query parameters
func (c *Client) getWithParams(ctx context.Context, path string, params url.Values, result interface{}) error {
	queryURL := c.baseURL + path
	if len(params) > 0 {
		queryURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(c.email, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse error response
		var errResp Error
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Msg != "" {
			if resp.StatusCode == http.StatusTooManyRequests {
				return fmt.Errorf("rate limited: %s", errResp.Msg)
			}
			return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, errResp.Msg)
		}
		// Fallback if we can't parse error response
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("rate limited by Zulip API")
		}
		return fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response (HTTP %d): %w", resp.StatusCode, err)
	}

	return nil
}

// delete sends a DELETE request to the Zulip API
func (c *Client) delete(ctx context.Context, path string, body interface{}, result interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.email, c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		var errResp Error
		if err := json.Unmarshal(respBody, &errResp); err == nil {
			return fmt.Errorf("API error: %s", errResp.Msg)
		}
		return fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	if result != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}
