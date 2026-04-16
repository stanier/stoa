package zulip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Config holds configuration for the Zulip MCP server.
type Config struct {
	BaseURL       string        // e.g. https://yourorg.zulipchat.com
	Email         string        // bot email
	APIKey        string        // bot API key
	Timeout       time.Duration
	AttachmentDir string // directory to save downloaded attachments
}

// Server implements the mcpserver.Server interface for Zulip.
type Server struct {
	mcpserver.BaseServer
	cfg         Config
	client      *http.Client
	handlers    map[string]mcpserver.ToolHandler
	streamCache map[string]int // cache stream name → stream ID
	cacheMu     sync.RWMutex
}

// narrow is a Zulip message filter pair.
type narrow struct {
	Operator string `json:"operator"`
	Operand  string `json:"operand"`
}

// buildNarrow serializes a slice of narrows as a JSON string for query params.
func buildNarrow(filters []narrow) string {
	b, _ := json.Marshal(filters)
	return string(b)
}

// NewServer creates a new Zulip MCP server.
func NewServer(name, version string, cfg Config) (*Server, error) {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.AttachmentDir == "" {
		cfg.AttachmentDir = "/mcp/root/attachments"
	}

	s := &Server{
		BaseServer:  mcpserver.BaseServer{Name: name, Version: version},
		cfg:         cfg,
		client:      &http.Client{Timeout: cfg.Timeout},
		handlers:    make(map[string]mcpserver.ToolHandler),
		streamCache: make(map[string]int),
	}

	s.handlers["zulip_send_message"] = s.handleSendMessage
	s.handlers["zulip_get_messages"] = s.handleGetMessages
	s.handlers["zulip_update_message"] = s.handleUpdateMessage
	s.handlers["zulip_delete_message"] = s.handleDeleteMessage
	s.handlers["zulip_add_reaction"] = s.handleAddReaction
	s.handlers["zulip_remove_reaction"] = s.handleRemoveReaction
	s.handlers["zulip_list_streams"] = s.handleListStreams
	s.handlers["zulip_get_stream"] = s.handleGetStream
	s.handlers["zulip_get_topics"] = s.handleGetTopics
	s.handlers["zulip_subscribe"] = s.handleSubscribe
	s.handlers["zulip_list_users"] = s.handleListUsers
	s.handlers["zulip_get_user"] = s.handleGetUser
	s.handlers["zulip_download_attachment"] = s.handleDownloadAttachment

	return s, nil
}

// ListTools returns all available Zulip tools.
func (s *Server) ListTools(ctx context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "zulip_send_message",
			Description: "Send a message to a Zulip stream or direct message to a user.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"stream", "direct"},
						"description": "Message type: 'stream' for a channel message, 'direct' for a direct message",
					},
					"to": map[string]any{
						"type":        "string",
						"description": "For stream messages: stream name. For direct messages: comma-separated email addresses.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The message content (supports Zulip Markdown)",
					},
					"topic": map[string]any{
						"type":        "string",
						"description": "Topic for stream messages (required when type=stream)",
					},
				},
				"required": []string{"type", "to", "content"},
			},
		},
		{
			Name:        "zulip_get_messages",
			Description: "Retrieve messages from Zulip, optionally filtered by stream, topic, sender, or search query.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stream": map[string]any{
						"type":        "string",
						"description": "Filter by stream name",
					},
					"topic": map[string]any{
						"type":        "string",
						"description": "Filter by topic (requires stream)",
					},
					"sender_email": map[string]any{
						"type":        "string",
						"description": "Filter by sender email address",
					},
					"search": map[string]any{
						"type":        "string",
						"description": "Full-text search query",
					},
					"anchor": map[string]any{
						"type":        "string",
						"description": "Message anchor: 'newest', 'oldest', or a numeric message ID (default: newest)",
					},
					"num_before": map[string]any{
						"type":        "integer",
						"description": "Number of messages before the anchor to return (default: 20)",
					},
					"num_after": map[string]any{
						"type":        "integer",
						"description": "Number of messages after the anchor to return (default: 0)",
					},
				},
			},
		},
		{
			Name:        "zulip_update_message",
			Description: "Edit the content or topic of a Zulip message.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{
						"type":        "integer",
						"description": "ID of the message to update",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "New message content",
					},
					"topic": map[string]any{
						"type":        "string",
						"description": "New topic for the message",
					},
					"propagate_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"change_one", "change_later", "change_all"},
						"description": "How to propagate topic changes: change_one (default), change_later, or change_all",
					},
				},
				"required": []string{"message_id"},
			},
		},
		{
			Name:        "zulip_delete_message",
			Description: "Delete a Zulip message by ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{
						"type":        "integer",
						"description": "ID of the message to delete",
					},
				},
				"required": []string{"message_id"},
			},
		},
		{
			Name:        "zulip_add_reaction",
			Description: "Add an emoji reaction to a Zulip message.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{
						"type":        "integer",
						"description": "ID of the message to react to",
					},
					"emoji_name": map[string]any{
						"type":        "string",
						"description": "Emoji name (e.g. 'thumbs_up', 'check', 'eyes')",
					},
				},
				"required": []string{"message_id", "emoji_name"},
			},
		},
		{
			Name:        "zulip_remove_reaction",
			Description: "Remove an emoji reaction from a Zulip message.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id": map[string]any{
						"type":        "integer",
						"description": "ID of the message to remove the reaction from",
					},
					"emoji_name": map[string]any{
						"type":        "string",
						"description": "Emoji name to remove",
					},
				},
				"required": []string{"message_id", "emoji_name"},
			},
		},
		{
			Name:        "zulip_list_streams",
			Description: "List Zulip streams (channels) with optional filtering.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"include_public": map[string]any{
						"type":        "boolean",
						"description": "Include public streams (default: true)",
					},
					"include_subscribed": map[string]any{
						"type":        "boolean",
						"description": "Include subscribed streams (default: true)",
					},
					"include_archived": map[string]any{
						"type":        "boolean",
						"description": "Include archived streams (default: false)",
					},
				},
			},
		},
		{
			Name:        "zulip_get_stream",
			Description: "Get detailed information about a specific Zulip stream by name.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stream_name": map[string]any{
						"type":        "string",
						"description": "Name of the stream to look up",
					},
				},
				"required": []string{"stream_name"},
			},
		},
		{
			Name:        "zulip_get_topics",
			Description: "List all topics in a Zulip stream.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stream_name": map[string]any{
						"type":        "string",
						"description": "Name of the stream to get topics for",
					},
				},
				"required": []string{"stream_name"},
			},
		},
		{
			Name:        "zulip_subscribe",
			Description: "Subscribe to one or more Zulip streams.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"streams": map[string]any{
						"type":        "string",
						"description": "Comma-separated list of stream names to subscribe to",
					},
				},
				"required": []string{"streams"},
			},
		},
		{
			Name:        "zulip_list_users",
			Description: "List Zulip users with optional filtering.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"include_bots": map[string]any{
						"type":        "boolean",
						"description": "Include bot accounts (default: false)",
					},
					"include_deactivated": map[string]any{
						"type":        "boolean",
						"description": "Include deactivated accounts (default: false)",
					},
				},
			},
		},
		{
			Name:        "zulip_get_user",
			Description: "Get details about a specific Zulip user by ID or email.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"user_id": map[string]any{
						"type":        "integer",
						"description": "User ID to look up",
					},
					"email": map[string]any{
						"type":        "string",
						"description": "User email to look up",
					},
				},
			},
		},
		{
			Name:        "zulip_download_attachment",
			Description: "Download a Zulip file attachment by its path_id and save it to the shared filesystem. Returns the path where the file was written.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path_id": map[string]any{
						"type":        "string",
						"description": "The path_id from the attachment metadata (e.g. '2/ab/xyz/filename.pdf')",
					},
					"filename": map[string]any{
						"type":        "string",
						"description": "Optional filename override. Defaults to the basename of path_id.",
					},
				},
				"required": []string{"path_id"},
			},
		},
	}, nil
}

// CallTool dispatches a tool call to the appropriate handler.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

// Shutdown cleans up resources.
func (s *Server) Shutdown(ctx context.Context) error {
	return nil
}

// --- HTTP helpers ---

func (s *Server) apiURL(path string) string {
	return s.cfg.BaseURL + "/api/v1/" + path
}

func (s *Server) doGet(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	reqURL := s.apiURL(path)
	if len(params) > 0 {
		reqURL = reqURL + "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(s.cfg.Email, s.cfg.APIKey)

	return s.doRequest(req)
}

func (s *Server) doPost(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL(path), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(s.cfg.Email, s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return s.doRequest(req)
}

func (s *Server) doPatch(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, s.apiURL(path), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(s.cfg.Email, s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return s.doRequest(req)
}

func (s *Server) doDelete(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	var bodyReader io.Reader
	if len(params) > 0 {
		bodyReader = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.apiURL(path), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(s.cfg.Email, s.cfg.APIKey)
	if len(params) > 0 {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	return s.doRequest(req)
}

func (s *Server) doRequest(req *http.Request) (map[string]any, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode response (status %d): %w", resp.StatusCode, err)
	}

	if resultField, ok := result["result"].(string); ok && resultField != "success" {
		msg, _ := result["msg"].(string)
		if msg == "" {
			msg = fmt.Sprintf("API error (status %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("zulip API error: %s", msg)
	}

	return result, nil
}

// getStreamID returns the stream ID for a stream name, using a local cache.
func (s *Server) getStreamID(ctx context.Context, streamName string) (int, error) {
	s.cacheMu.RLock()
	if id, ok := s.streamCache[streamName]; ok {
		s.cacheMu.RUnlock()
		return id, nil
	}
	s.cacheMu.RUnlock()

	params := url.Values{}
	params.Set("stream", streamName)
	result, err := s.doGet(ctx, "get_stream_id", params)
	if err != nil {
		return 0, fmt.Errorf("get stream id for %q: %w", streamName, err)
	}

	idFloat, ok := result["stream_id"].(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected stream_id type in response")
	}
	id := int(idFloat)

	s.cacheMu.Lock()
	s.streamCache[streamName] = id
	s.cacheMu.Unlock()

	return id, nil
}

// --- Tool handlers ---

func (s *Server) handleSendMessage(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	msgType, ok := args["type"].(string)
	if !ok || msgType == "" {
		return mcpserver.ErrorResult("type is required (stream or direct)"), nil
	}
	to, ok := args["to"].(string)
	if !ok || to == "" {
		return mcpserver.ErrorResult("to is required"), nil
	}
	content, ok := args["content"].(string)
	if !ok || content == "" {
		return mcpserver.ErrorResult("content is required"), nil
	}

	params := url.Values{}
	params.Set("type", msgType)
	params.Set("content", content)

	switch msgType {
	case "stream":
		params.Set("to", to)
		topic, _ := args["topic"].(string)
		if topic == "" {
			return mcpserver.ErrorResult("topic is required for stream messages"), nil
		}
		params.Set("topic", topic)
	case "direct":
		// Zulip wants a JSON array of email strings
		emails := strings.Split(to, ",")
		for i := range emails {
			emails[i] = strings.TrimSpace(emails[i])
		}
		emailsJSON, _ := json.Marshal(emails)
		params.Set("to", string(emailsJSON))
	default:
		return mcpserver.ErrorResult("type must be 'stream' or 'direct'"), nil
	}

	result, err := s.doPost(ctx, "messages", params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("send message: %v", err)), nil
	}

	idFloat, _ := result["id"].(float64)
	return mcpserver.TextResult(fmt.Sprintf("Message sent. ID: %d", int(idFloat))), nil
}

func (s *Server) handleGetMessages(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	anchor := "newest"
	if a, ok := args["anchor"].(string); ok && a != "" {
		anchor = a
	}

	numBefore := 20
	if raw, ok := args["num_before"]; ok {
		if n := toInt(raw); n >= 0 {
			numBefore = n
		}
	}

	numAfter := 0
	if raw, ok := args["num_after"]; ok {
		if n := toInt(raw); n >= 0 {
			numAfter = n
		}
	}

	var narrows []narrow
	if stream, ok := args["stream"].(string); ok && stream != "" {
		narrows = append(narrows, narrow{Operator: "stream", Operand: stream})
	}
	if topic, ok := args["topic"].(string); ok && topic != "" {
		narrows = append(narrows, narrow{Operator: "topic", Operand: topic})
	}
	if sender, ok := args["sender_email"].(string); ok && sender != "" {
		narrows = append(narrows, narrow{Operator: "sender", Operand: sender})
	}
	if search, ok := args["search"].(string); ok && search != "" {
		narrows = append(narrows, narrow{Operator: "search", Operand: search})
	}

	params := url.Values{}
	params.Set("anchor", anchor)
	params.Set("num_before", fmt.Sprintf("%d", numBefore))
	params.Set("num_after", fmt.Sprintf("%d", numAfter))
	params.Set("apply_markdown", "false")
	if len(narrows) > 0 {
		params.Set("narrow", buildNarrow(narrows))
	}

	result, err := s.doGet(ctx, "messages", params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get messages: %v", err)), nil
	}

	messages, _ := result["messages"].([]interface{})
	if len(messages) == 0 {
		return mcpserver.TextResult("No messages found."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d message(s):\n\n", len(messages)))
	for _, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		id := int(toFloat(msg["id"]))
		senderName, _ := msg["sender_full_name"].(string)
		streamName, _ := msg["display_recipient"].(string)
		// display_recipient can be an array for DMs
		if arr, ok := msg["display_recipient"].([]interface{}); ok {
			var names []string
			for _, u := range arr {
				if um, ok := u.(map[string]interface{}); ok {
					if n, ok := um["full_name"].(string); ok {
						names = append(names, n)
					}
				}
			}
			streamName = strings.Join(names, ", ")
		}
		topic, _ := msg["subject"].(string)
		content, _ := msg["content"].(string)
		ts := int64(toFloat(msg["timestamp"]))
		timestamp := time.Unix(ts, 0).UTC().Format(time.RFC3339)

		sb.WriteString(fmt.Sprintf("[%d] %s in #%s > %s at %s:\n%s\n\n",
			id, senderName, streamName, topic, timestamp, content))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleUpdateMessage(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	idRaw, ok := args["message_id"]
	if !ok {
		return mcpserver.ErrorResult("message_id is required"), nil
	}
	messageID := int(toFloat(idRaw))

	params := url.Values{}
	if content, ok := args["content"].(string); ok && content != "" {
		params.Set("content", content)
	}
	if topic, ok := args["topic"].(string); ok && topic != "" {
		params.Set("topic", topic)
	}
	if mode, ok := args["propagate_mode"].(string); ok && mode != "" {
		params.Set("propagate_mode", mode)
	}

	if len(params) == 0 {
		return mcpserver.ErrorResult("at least one of content or topic must be provided"), nil
	}

	_, err := s.doPatch(ctx, fmt.Sprintf("messages/%d", messageID), params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("update message: %v", err)), nil
	}

	return mcpserver.TextResult(fmt.Sprintf("Message %d updated.", messageID)), nil
}

func (s *Server) handleDeleteMessage(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	idRaw, ok := args["message_id"]
	if !ok {
		return mcpserver.ErrorResult("message_id is required"), nil
	}
	messageID := int(toFloat(idRaw))

	_, err := s.doDelete(ctx, fmt.Sprintf("messages/%d", messageID), nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("delete message: %v", err)), nil
	}

	return mcpserver.TextResult(fmt.Sprintf("Message %d deleted.", messageID)), nil
}

func (s *Server) handleAddReaction(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	idRaw, ok := args["message_id"]
	if !ok {
		return mcpserver.ErrorResult("message_id is required"), nil
	}
	messageID := int(toFloat(idRaw))

	emojiName, ok := args["emoji_name"].(string)
	if !ok || emojiName == "" {
		return mcpserver.ErrorResult("emoji_name is required"), nil
	}

	params := url.Values{}
	params.Set("emoji_name", emojiName)
	params.Set("reaction_type", "unicode_emoji")

	_, err := s.doPost(ctx, fmt.Sprintf("messages/%d/reactions", messageID), params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("add reaction: %v", err)), nil
	}

	return mcpserver.TextResult(fmt.Sprintf("Reaction :%s: added to message %d.", emojiName, messageID)), nil
}

func (s *Server) handleRemoveReaction(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	idRaw, ok := args["message_id"]
	if !ok {
		return mcpserver.ErrorResult("message_id is required"), nil
	}
	messageID := int(toFloat(idRaw))

	emojiName, ok := args["emoji_name"].(string)
	if !ok || emojiName == "" {
		return mcpserver.ErrorResult("emoji_name is required"), nil
	}

	params := url.Values{}
	params.Set("emoji_name", emojiName)
	params.Set("reaction_type", "unicode_emoji")

	_, err := s.doDelete(ctx, fmt.Sprintf("messages/%d/reactions", messageID), params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("remove reaction: %v", err)), nil
	}

	return mcpserver.TextResult(fmt.Sprintf("Reaction :%s: removed from message %d.", emojiName, messageID)), nil
}

func (s *Server) handleListStreams(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	includePublic := true
	if v, ok := args["include_public"].(bool); ok {
		includePublic = v
	}
	includeSubscribed := true
	if v, ok := args["include_subscribed"].(bool); ok {
		includeSubscribed = v
	}
	includeArchived := false
	if v, ok := args["include_archived"].(bool); ok {
		includeArchived = v
	}

	params := url.Values{}
	params.Set("include_public", boolStr(includePublic))
	params.Set("include_subscribed", boolStr(includeSubscribed))
	params.Set("include_archived", boolStr(includeArchived))

	result, err := s.doGet(ctx, "streams", params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("list streams: %v", err)), nil
	}

	streams, _ := result["streams"].([]interface{})
	if len(streams) == 0 {
		return mcpserver.TextResult("No streams found."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d stream(s):\n\n", len(streams)))
	for _, st := range streams {
		stream, ok := st.(map[string]interface{})
		if !ok {
			continue
		}
		id := int(toFloat(stream["stream_id"]))
		name, _ := stream["name"].(string)
		desc, _ := stream["description"].(string)
		inviteOnly, _ := stream["invite_only"].(bool)
		sb.WriteString(fmt.Sprintf("[%d] %s — %s (private: %v)\n", id, name, desc, inviteOnly))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetStream(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	streamName, ok := args["stream_name"].(string)
	if !ok || streamName == "" {
		return mcpserver.ErrorResult("stream_name is required"), nil
	}

	streamID, err := s.getStreamID(ctx, streamName)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get stream: %v", err)), nil
	}

	result, err := s.doGet(ctx, fmt.Sprintf("streams/%d", streamID), nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get stream details: %v", err)), nil
	}

	stream, ok := result["stream"].(map[string]interface{})
	if !ok {
		return mcpserver.ErrorResult("unexpected response format"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Stream: %s\n", streamName))
	sb.WriteString(fmt.Sprintf("ID: %d\n", streamID))
	if desc, ok := stream["description"].(string); ok {
		sb.WriteString(fmt.Sprintf("Description: %s\n", desc))
	}
	if inviteOnly, ok := stream["invite_only"].(bool); ok {
		sb.WriteString(fmt.Sprintf("Private: %v\n", inviteOnly))
	}
	if isWebPublic, ok := stream["is_web_public"].(bool); ok {
		sb.WriteString(fmt.Sprintf("Web public: %v\n", isWebPublic))
	}
	if histPublic, ok := stream["history_public_to_subscribers"].(bool); ok {
		sb.WriteString(fmt.Sprintf("History public to subscribers: %v\n", histPublic))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetTopics(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	streamName, ok := args["stream_name"].(string)
	if !ok || streamName == "" {
		return mcpserver.ErrorResult("stream_name is required"), nil
	}

	streamID, err := s.getStreamID(ctx, streamName)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get stream: %v", err)), nil
	}

	result, err := s.doGet(ctx, fmt.Sprintf("users/me/%d/topics", streamID), nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get topics: %v", err)), nil
	}

	topics, _ := result["topics"].([]interface{})
	if len(topics) == 0 {
		return mcpserver.TextResult(fmt.Sprintf("No topics found in stream %q.", streamName)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Topics in #%s (%d):\n\n", streamName, len(topics)))
	for _, t := range topics {
		topic, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := topic["name"].(string)
		maxID := int(toFloat(topic["max_id"]))
		sb.WriteString(fmt.Sprintf("%s (last message: %d)\n", name, maxID))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleSubscribe(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	streamsStr, ok := args["streams"].(string)
	if !ok || streamsStr == "" {
		return mcpserver.ErrorResult("streams is required (comma-separated stream names)"), nil
	}

	names := strings.Split(streamsStr, ",")
	type sub struct {
		Name string `json:"name"`
	}
	subs := make([]sub, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			subs = append(subs, sub{Name: n})
		}
	}

	subsJSON, _ := json.Marshal(subs)

	params := url.Values{}
	params.Set("subscriptions", string(subsJSON))

	result, err := s.doPost(ctx, "users/me/subscriptions", params)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("subscribe: %v", err)), nil
	}

	var sb strings.Builder

	if subscribed, ok := result["subscribed"].(map[string]interface{}); ok {
		for email, rawStreams := range subscribed {
			if streams, ok := rawStreams.([]interface{}); ok {
				for _, st := range streams {
					if name, ok := st.(string); ok {
						sb.WriteString(fmt.Sprintf("Newly subscribed %s to: %s\n", email, name))
					}
				}
			}
		}
	}

	if alreadySubscribed, ok := result["already_subscribed"].(map[string]interface{}); ok {
		for email, rawStreams := range alreadySubscribed {
			if streams, ok := rawStreams.([]interface{}); ok {
				for _, st := range streams {
					if name, ok := st.(string); ok {
						sb.WriteString(fmt.Sprintf("Already subscribed %s to: %s\n", email, name))
					}
				}
			}
		}
	}

	if sb.Len() == 0 {
		sb.WriteString("Subscription request processed.")
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleListUsers(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	includeBots := false
	if v, ok := args["include_bots"].(bool); ok {
		includeBots = v
	}
	includeDeactivated := false
	if v, ok := args["include_deactivated"].(bool); ok {
		includeDeactivated = v
	}

	result, err := s.doGet(ctx, "users", nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("list users: %v", err)), nil
	}

	members, _ := result["members"].([]interface{})

	var sb strings.Builder
	count := 0
	for _, m := range members {
		user, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		isBot, _ := user["is_bot"].(bool)
		isActive, _ := user["is_active"].(bool)

		if isBot && !includeBots {
			continue
		}
		if !isActive && !includeDeactivated {
			continue
		}

		id := int(toFloat(user["user_id"]))
		name, _ := user["full_name"].(string)
		email, _ := user["email"].(string)
		sb.WriteString(fmt.Sprintf("[%d] %s <%s> (bot: %v, active: %v)\n", id, name, email, isBot, isActive))
		count++
	}

	if count == 0 {
		return mcpserver.TextResult("No users found matching the filter."), nil
	}

	header := fmt.Sprintf("Found %d user(s):\n\n", count)
	return mcpserver.TextResult(header + sb.String()), nil
}

func (s *Server) handleGetUser(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	var path string

	if idRaw, ok := args["user_id"]; ok && idRaw != nil {
		userID := int(toFloat(idRaw))
		path = fmt.Sprintf("users/%d", userID)
	} else if email, ok := args["email"].(string); ok && email != "" {
		path = fmt.Sprintf("users/%s", url.PathEscape(email))
	} else {
		return mcpserver.ErrorResult("either user_id or email is required"), nil
	}

	result, err := s.doGet(ctx, path, nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get user: %v", err)), nil
	}

	user, ok := result["user"].(map[string]interface{})
	if !ok {
		return mcpserver.ErrorResult("unexpected response format"), nil
	}

	var sb strings.Builder
	if id, ok := user["user_id"].(float64); ok {
		sb.WriteString(fmt.Sprintf("ID: %d\n", int(id)))
	}
	if name, ok := user["full_name"].(string); ok {
		sb.WriteString(fmt.Sprintf("Name: %s\n", name))
	}
	if email, ok := user["email"].(string); ok {
		sb.WriteString(fmt.Sprintf("Email: %s\n", email))
	}
	if isBot, ok := user["is_bot"].(bool); ok {
		sb.WriteString(fmt.Sprintf("Bot: %v\n", isBot))
	}
	if isActive, ok := user["is_active"].(bool); ok {
		sb.WriteString(fmt.Sprintf("Active: %v\n", isActive))
	}
	if role, ok := user["role"].(float64); ok {
		sb.WriteString(fmt.Sprintf("Role: %d\n", int(role)))
	}
	if timezone, ok := user["timezone"].(string); ok && timezone != "" {
		sb.WriteString(fmt.Sprintf("Timezone: %s\n", timezone))
	}
	if dateJoined, ok := user["date_joined"].(string); ok {
		sb.WriteString(fmt.Sprintf("Date joined: %s\n", dateJoined))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleDownloadAttachment(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	pathID, _ := args["path_id"].(string)
	if pathID == "" {
		return mcpserver.ErrorResult("path_id is required"), nil
	}
	filename, _ := args["filename"].(string)
	if filename == "" {
		filename = filepath.Base(pathID)
	}
	filename = filepath.Base(strings.ReplaceAll(filename, " ", "_"))

	if err := os.MkdirAll(s.cfg.AttachmentDir, 0755); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("create dir: %v", err)), nil
	}
	destPath := filepath.Join(s.cfg.AttachmentDir, filename)

	// Step 1: call /api/v1/user_uploads/<path_id> to get a temporary signed URL.
	// This is the correct API endpoint; the web path /user_uploads/ requires
	// session auth rather than basic auth on Zulip Cloud / S3-backed installs.
	apiEndpoint := s.apiURL("user_uploads/" + pathID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiEndpoint, nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("build request: %v", err)), nil
	}
	req.SetBasicAuth(s.cfg.Email, s.cfg.APIKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get temp URL (%s): %v", apiEndpoint, err)), nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mcpserver.ErrorResult(fmt.Sprintf("get temp URL %s HTTP %d: %s", apiEndpoint, resp.StatusCode, string(body))), nil
	}

	// Parse the JSON response: {"url": "...", "result": "success", "msg": ""}
	var urlResp struct {
		Result string `json:"result"`
		Msg    string `json:"msg"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(body, &urlResp); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse temp URL response (body: %s): %v", string(body), err)), nil
	}
	if urlResp.Result != "success" {
		return mcpserver.ErrorResult(fmt.Sprintf("get temp URL: %s", urlResp.Msg)), nil
	}
	if urlResp.URL == "" {
		return mcpserver.ErrorResult(fmt.Sprintf("get temp URL: empty url in response (body: %s)", string(body))), nil
	}

	// The URL may be relative (local storage) or absolute (S3 pre-signed).
	fileURL := urlResp.URL
	if !strings.HasPrefix(fileURL, "http") {
		fileURL = s.cfg.BaseURL + fileURL
	}

	// Step 2: download the actual file content from the signed URL.
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("build download request: %v", err)), nil
	}
	resp2, err := s.client.Do(req2)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("download (%s): %v", fileURL, err)), nil
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp2.Body)
		return mcpserver.ErrorResult(fmt.Sprintf("download %s HTTP %d: %s", fileURL, resp2.StatusCode, string(errBody))), nil
	}
	data, err := io.ReadAll(resp2.Body)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("read body: %v", err)), nil
	}
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("write file: %v", err)), nil
	}
	return mcpserver.TextResult(fmt.Sprintf("Saved to: %s (%d bytes)", destPath, len(data))), nil
}

// --- Helpers ---



// toFloat converts a JSON-decoded value to float64.
func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// toInt converts a JSON-decoded number to int.
func toInt(v interface{}) int {
	return int(toFloat(v))
}

// boolStr converts a bool to "true"/"false" string for URL params.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
