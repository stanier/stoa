package trello

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

const baseURL = "https://api.trello.com/1"

// Config holds Trello API credentials and access restrictions.
type Config struct {
	APIKey        string
	Token         string
	AllowedBoards []string // if non-empty, only these board IDs are accessible
}

// Server implements the MCP Trello server.
type Server struct {
	mcpserver.BaseServer
	cfg           Config
	client        *http.Client
	handlers      map[string]mcpserver.ToolHandler
	allowedBoards map[string]bool // nil means no restriction
}

// NewServer creates a new Trello MCP server.
func NewServer(name, version string, cfg Config) *Server {
	var allowed map[string]bool
	if len(cfg.AllowedBoards) > 0 {
		allowed = make(map[string]bool, len(cfg.AllowedBoards))
		for _, id := range cfg.AllowedBoards {
			allowed[id] = true
		}
	}

	s := &Server{
		BaseServer:    mcpserver.BaseServer{Name: name, Version: version},
		cfg:           cfg,
		client:        &http.Client{Timeout: 30 * time.Second},
		handlers:      make(map[string]mcpserver.ToolHandler),
		allowedBoards: allowed,
	}

	// Read-only tools
	s.handlers["get_member"] = s.handleGetMember
	s.handlers["list_boards"] = s.handleListBoards
	s.handlers["get_board"] = s.handleGetBoard
	s.handlers["get_board_members"] = s.handleGetBoardMembers
	s.handlers["get_board_labels"] = s.handleGetBoardLabels
	s.handlers["list_lists"] = s.handleListLists
	s.handlers["list_cards_on_board"] = s.handleListCardsOnBoard
	s.handlers["list_cards_on_list"] = s.handleListCardsOnList
	s.handlers["get_card"] = s.handleGetCard
	s.handlers["get_card_comments"] = s.handleGetCardComments

	// Write tools
	s.handlers["create_card"] = s.handleCreateCard
	s.handlers["update_card"] = s.handleUpdateCard
	s.handlers["delete_card"] = s.handleDeleteCard
	s.handlers["create_checklist"] = s.handleCreateChecklist
	s.handlers["add_checklist_item"] = s.handleAddChecklistItem
	s.handlers["update_checklist_item"] = s.handleUpdateChecklistItem
	s.handlers["delete_checklist"] = s.handleDeleteChecklist
	s.handlers["add_comment"] = s.handleAddComment
	s.handlers["delete_comment"] = s.handleDeleteComment
	s.handlers["add_label_to_card"] = s.handleAddLabelToCard
	s.handlers["remove_label_from_card"] = s.handleRemoveLabelFromCard
	s.handlers["create_label"] = s.handleCreateLabel
	s.handlers["assign_member_to_card"] = s.handleAssignMemberToCard
	s.handlers["remove_member_from_card"] = s.handleRemoveMemberFromCard
	s.handlers["add_url_attachment"] = s.handleAddURLAttachment
	s.handlers["create_list"] = s.handleCreateList
	s.handlers["archive_list"] = s.handleArchiveList

	return s
}

func (s *Server) Shutdown(_ context.Context) error { return nil }

func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		// ── Read-only ──────────────────────────────────────────────────────────
		{
			Name:        "get_member",
			Description: "Get information about a Trello member. Use 'me' as the member ID to get the authenticated user.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"member_id": map[string]interface{}{
						"type":        "string",
						"description": "Member ID or username ('me' for authenticated user)",
					},
				},
				"required": []string{"member_id"},
			},
		},
		{
			Name:        "list_boards",
			Description: "List all Trello boards for a member. Use 'me' to list boards for the authenticated user.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"member_id": map[string]interface{}{
						"type":        "string",
						"description": "Member ID or username ('me' for authenticated user)",
					},
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Filter boards: 'open', 'closed', 'all' (default: open)",
					},
				},
				"required": []string{"member_id"},
			},
		},
		{
			Name:        "get_board",
			Description: "Get details about a specific Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
				},
				"required": []string{"board_id"},
			},
		},
		{
			Name:        "get_board_members",
			Description: "List all members of a Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
				},
				"required": []string{"board_id"},
			},
		},
		{
			Name:        "get_board_labels",
			Description: "List all labels defined on a Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
				},
				"required": []string{"board_id"},
			},
		},
		{
			Name:        "list_lists",
			Description: "List all lists on a Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Filter lists: 'open', 'closed', 'all' (default: open)",
					},
				},
				"required": []string{"board_id"},
			},
		},
		{
			Name:        "list_cards_on_board",
			Description: "List all cards on a Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
					"filter": map[string]interface{}{
						"type":        "string",
						"description": "Filter cards: 'open', 'closed', 'all' (default: open)",
					},
				},
				"required": []string{"board_id"},
			},
		},
		{
			Name:        "list_cards_on_list",
			Description: "List all cards in a specific Trello list.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"list_id": map[string]interface{}{"type": "string", "description": "List ID"},
				},
				"required": []string{"list_id"},
			},
		},
		{
			Name:        "get_card",
			Description: "Get full details about a specific Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
				},
				"required": []string{"card_id"},
			},
		},
		{
			Name:        "get_card_comments",
			Description: "List comments on a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
				},
				"required": []string{"card_id"},
			},
		},
		// ── Write ──────────────────────────────────────────────────────────────
		{
			Name:        "create_card",
			Description: "Create a new card in a Trello list.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"list_id":  map[string]interface{}{"type": "string", "description": "List ID to create the card in"},
					"name":     map[string]interface{}{"type": "string", "description": "Card name/title"},
					"desc":     map[string]interface{}{"type": "string", "description": "Card description (optional)"},
					"due":      map[string]interface{}{"type": "string", "description": "Due date in ISO 8601 format (optional)"},
					"label_ids": map[string]interface{}{"type": "string", "description": "Comma-separated label IDs to apply (optional)"},
					"member_ids": map[string]interface{}{"type": "string", "description": "Comma-separated member IDs to assign (optional)"},
					"pos": map[string]interface{}{"type": "string", "description": "Position: 'top', 'bottom', or a positive number (optional)"},
				},
				"required": []string{"list_id", "name"},
			},
		},
		{
			Name:        "update_card",
			Description: "Update fields on an existing Trello card. Only provided fields are changed. Supply list_id (and optionally board_id) to move the card. Set closed=true to archive it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":      map[string]interface{}{"type": "string", "description": "Card ID"},
					"name":         map[string]interface{}{"type": "string", "description": "New card name (optional)"},
					"desc":         map[string]interface{}{"type": "string", "description": "New description (optional)"},
					"due":          map[string]interface{}{"type": "string", "description": "New due date in ISO 8601 format, or empty string to clear (optional)"},
					"pos":          map[string]interface{}{"type": "string", "description": "New position: 'top', 'bottom', or a positive number (optional)"},
					"due_complete": map[string]interface{}{"type": "boolean", "description": "Mark due date as complete/incomplete (optional)"},
					"list_id":      map[string]interface{}{"type": "string", "description": "Move card to this list ID (optional)"},
					"board_id":     map[string]interface{}{"type": "string", "description": "Destination board ID when moving to a different board (optional, requires list_id)"},
					"closed":       map[string]interface{}{"type": "boolean", "description": "Archive (true) or unarchive (false) the card (optional)"},
				},
				"required": []string{"card_id"},
			},
		},
		{
			Name:        "delete_card",
			Description: "Permanently delete a Trello card. This cannot be undone.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
				},
				"required": []string{"card_id"},
			},
		},
		{
			Name:        "create_checklist",
			Description: "Create a new checklist on a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
					"name":    map[string]interface{}{"type": "string", "description": "Checklist name"},
				},
				"required": []string{"card_id", "name"},
			},
		},
		{
			Name:        "add_checklist_item",
			Description: "Add an item to an existing checklist.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"checklist_id": map[string]interface{}{"type": "string", "description": "Checklist ID"},
					"name":         map[string]interface{}{"type": "string", "description": "Item name"},
					"checked":      map[string]interface{}{"type": "boolean", "description": "Initial checked state (default: false)"},
					"pos":          map[string]interface{}{"type": "string", "description": "Position: 'top', 'bottom', or a number (optional)"},
				},
				"required": []string{"checklist_id", "name"},
			},
		},
		{
			Name:        "update_checklist_item",
			Description: "Update the state or name of a checklist item.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":         map[string]interface{}{"type": "string", "description": "Card ID that owns the checklist"},
					"checklist_item_id": map[string]interface{}{"type": "string", "description": "Checklist item ID"},
					"state":           map[string]interface{}{"type": "string", "description": "Item state: 'complete' or 'incomplete'"},
					"name":            map[string]interface{}{"type": "string", "description": "New item name (optional)"},
				},
				"required": []string{"card_id", "checklist_item_id"},
			},
		},
		{
			Name:        "delete_checklist",
			Description: "Delete a checklist from a card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"checklist_id": map[string]interface{}{"type": "string", "description": "Checklist ID"},
				},
				"required": []string{"checklist_id"},
			},
		},
		{
			Name:        "add_comment",
			Description: "Add a comment to a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
					"text":    map[string]interface{}{"type": "string", "description": "Comment text"},
				},
				"required": []string{"card_id", "text"},
			},
		},
		{
			Name:        "delete_comment",
			Description: "Delete a comment from a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":   map[string]interface{}{"type": "string", "description": "Card ID"},
					"action_id": map[string]interface{}{"type": "string", "description": "Comment action ID"},
				},
				"required": []string{"card_id", "action_id"},
			},
		},
		{
			Name:        "add_label_to_card",
			Description: "Apply an existing label to a card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":  map[string]interface{}{"type": "string", "description": "Card ID"},
					"label_id": map[string]interface{}{"type": "string", "description": "Label ID"},
				},
				"required": []string{"card_id", "label_id"},
			},
		},
		{
			Name:        "remove_label_from_card",
			Description: "Remove a label from a card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":  map[string]interface{}{"type": "string", "description": "Card ID"},
					"label_id": map[string]interface{}{"type": "string", "description": "Label ID"},
				},
				"required": []string{"card_id", "label_id"},
			},
		},
		{
			Name:        "create_label",
			Description: "Create a new label on a board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
					"name":     map[string]interface{}{"type": "string", "description": "Label name"},
					"color": map[string]interface{}{
						"type":        "string",
						"description": "Label color: green, yellow, orange, red, purple, blue, sky, lime, pink, black, or empty string for no color",
					},
				},
				"required": []string{"board_id", "name"},
			},
		},
		{
			Name:        "assign_member_to_card",
			Description: "Assign a member to a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":   map[string]interface{}{"type": "string", "description": "Card ID"},
					"member_id": map[string]interface{}{"type": "string", "description": "Member ID"},
				},
				"required": []string{"card_id", "member_id"},
			},
		},
		{
			Name:        "remove_member_from_card",
			Description: "Remove a member from a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id":   map[string]interface{}{"type": "string", "description": "Card ID"},
					"member_id": map[string]interface{}{"type": "string", "description": "Member ID"},
				},
				"required": []string{"card_id", "member_id"},
			},
		},
		{
			Name:        "add_url_attachment",
			Description: "Attach a URL to a Trello card.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"card_id": map[string]interface{}{"type": "string", "description": "Card ID"},
					"url":     map[string]interface{}{"type": "string", "description": "URL to attach"},
					"name":    map[string]interface{}{"type": "string", "description": "Display name for the attachment (optional)"},
				},
				"required": []string{"card_id", "url"},
			},
		},
		{
			Name:        "create_list",
			Description: "Create a new list on a Trello board.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"board_id": map[string]interface{}{"type": "string", "description": "Board ID"},
					"name":     map[string]interface{}{"type": "string", "description": "List name"},
					"pos":      map[string]interface{}{"type": "string", "description": "Position: 'top', 'bottom', or a number (optional)"},
				},
				"required": []string{"board_id", "name"},
			},
		},
		{
			Name:        "archive_list",
			Description: "Archive (close) a Trello list.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"list_id": map[string]interface{}{"type": "string", "description": "List ID"},
				},
				"required": []string{"list_id"},
			},
		},
	}, nil
}

// ── Read-only handlers ─────────────────────────────────────────────────────

func (s *Server) handleGetMember(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "member_id", "")
	if id == "" {
		return mcpserver.ErrorResult("member_id is required"), nil
	}
	return s.get(ctx, fmt.Sprintf("/members/%s", id), nil)
}

func (s *Server) handleListBoards(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "member_id", "")
	if id == "" {
		return mcpserver.ErrorResult("member_id is required"), nil
	}
	filter := mcpserver.StrArg(args, "filter", "open")
	result, err := s.get(ctx, fmt.Sprintf("/members/%s/boards", id), url.Values{"filter": {filter}})
	if err != nil || result.IsError || s.allowedBoards == nil {
		return result, err
	}
	// Filter to only allowed boards.
	if len(result.Content) == 0 {
		return result, nil
	}
	var boards []map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(result.Content[0].Text), &boards); jsonErr != nil {
		return result, nil // not JSON we can parse; return as-is
	}
	filtered := boards[:0]
	for _, b := range boards {
		bid, _ := b["id"].(string)
		if s.allowedBoards[bid] {
			filtered = append(filtered, b)
		}
	}
	var pretty bytes.Buffer
	if jsonErr := json.Indent(&pretty, mustMarshal(filtered), "", "  "); jsonErr == nil {
		return mcpserver.TextResult(pretty.String()), nil
	}
	return mcpserver.TextResult(string(mustMarshal(filtered))), nil
}

func (s *Server) handleGetBoard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "board_id", "")
	if id == "" {
		return mcpserver.ErrorResult("board_id is required"), nil
	}
	if r := s.checkBoard(id); r != nil {
		return r, nil
	}
	return s.get(ctx, fmt.Sprintf("/boards/%s", id), nil)
}

func (s *Server) handleGetBoardMembers(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "board_id", "")
	if id == "" {
		return mcpserver.ErrorResult("board_id is required"), nil
	}
	if r := s.checkBoard(id); r != nil {
		return r, nil
	}
	return s.get(ctx, fmt.Sprintf("/boards/%s/members", id), nil)
}

func (s *Server) handleGetBoardLabels(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "board_id", "")
	if id == "" {
		return mcpserver.ErrorResult("board_id is required"), nil
	}
	if r := s.checkBoard(id); r != nil {
		return r, nil
	}
	return s.get(ctx, fmt.Sprintf("/boards/%s/labels", id), nil)
}

func (s *Server) handleListLists(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "board_id", "")
	if id == "" {
		return mcpserver.ErrorResult("board_id is required"), nil
	}
	if r := s.checkBoard(id); r != nil {
		return r, nil
	}
	filter := mcpserver.StrArg(args, "filter", "open")
	return s.get(ctx, fmt.Sprintf("/boards/%s/lists", id), url.Values{"filter": {filter}})
}

func (s *Server) handleListCardsOnBoard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "board_id", "")
	if id == "" {
		return mcpserver.ErrorResult("board_id is required"), nil
	}
	if r := s.checkBoard(id); r != nil {
		return r, nil
	}
	filter := mcpserver.StrArg(args, "filter", "open")
	return s.get(ctx, fmt.Sprintf("/boards/%s/cards/%s", id, filter), nil)
}

func (s *Server) handleListCardsOnList(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "list_id", "")
	if id == "" {
		return mcpserver.ErrorResult("list_id is required"), nil
	}
	return s.get(ctx, fmt.Sprintf("/lists/%s/cards", id), nil)
}

func (s *Server) handleGetCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "card_id", "")
	if id == "" {
		return mcpserver.ErrorResult("card_id is required"), nil
	}
	return s.get(ctx, fmt.Sprintf("/cards/%s", id), url.Values{
		"attachments": {"true"},
		"members":     {"true"},
		"checklists":  {"all"},
		"labels":      {"true"},
	})
}

func (s *Server) handleGetCardComments(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "card_id", "")
	if id == "" {
		return mcpserver.ErrorResult("card_id is required"), nil
	}
	return s.get(ctx, fmt.Sprintf("/cards/%s/actions", id), url.Values{"filter": {"commentCard"}})
}

// ── Write handlers ─────────────────────────────────────────────────────────

func (s *Server) handleCreateCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	listID := mcpserver.StrArg(args, "list_id", "")
	name := mcpserver.StrArg(args, "name", "")
	if listID == "" || name == "" {
		return mcpserver.ErrorResult("list_id and name are required"), nil
	}
	body := url.Values{"idList": {listID}, "name": {name}}
	if v := mcpserver.StrArg(args, "desc", ""); v != "" {
		body.Set("desc", v)
	}
	if v := mcpserver.StrArg(args, "due", ""); v != "" {
		body.Set("due", v)
	}
	if v := mcpserver.StrArg(args, "label_ids", ""); v != "" {
		body.Set("idLabels", v)
	}
	if v := mcpserver.StrArg(args, "member_ids", ""); v != "" {
		body.Set("idMembers", v)
	}
	if v := mcpserver.StrArg(args, "pos", ""); v != "" {
		body.Set("pos", v)
	}
	return s.post(ctx, "/cards", body)
}

func (s *Server) handleUpdateCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "card_id", "")
	if id == "" {
		return mcpserver.ErrorResult("card_id is required"), nil
	}
	body := url.Values{}
	if v := mcpserver.StrArg(args, "name", ""); v != "" {
		body.Set("name", v)
	}
	if v, ok := args["desc"]; ok {
		body.Set("desc", fmt.Sprintf("%v", v))
	}
	if v, ok := args["due"]; ok {
		body.Set("due", fmt.Sprintf("%v", v))
	}
	if v := mcpserver.StrArg(args, "pos", ""); v != "" {
		body.Set("pos", v)
	}
	if v, ok := args["due_complete"]; ok {
		body.Set("dueComplete", fmt.Sprintf("%v", v))
	}
	if v := mcpserver.StrArg(args, "list_id", ""); v != "" {
		body.Set("idList", v)
	}
	if v := mcpserver.StrArg(args, "board_id", ""); v != "" {
		if r := s.checkBoard(v); r != nil {
			return r, nil
		}
		body.Set("idBoard", v)
	}
	if v, ok := args["closed"]; ok {
		body.Set("closed", fmt.Sprintf("%v", v))
	}
	if len(body) == 0 {
		return mcpserver.ErrorResult("no fields to update"), nil
	}
	return s.put(ctx, fmt.Sprintf("/cards/%s", id), body)
}

func (s *Server) handleDeleteCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "card_id", "")
	if id == "" {
		return mcpserver.ErrorResult("card_id is required"), nil
	}
	return s.delete(ctx, fmt.Sprintf("/cards/%s", id))
}

func (s *Server) handleCreateChecklist(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	name := mcpserver.StrArg(args, "name", "")
	if cardID == "" || name == "" {
		return mcpserver.ErrorResult("card_id and name are required"), nil
	}
	return s.post(ctx, "/checklists", url.Values{"idCard": {cardID}, "name": {name}})
}

func (s *Server) handleAddChecklistItem(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	clID := mcpserver.StrArg(args, "checklist_id", "")
	name := mcpserver.StrArg(args, "name", "")
	if clID == "" || name == "" {
		return mcpserver.ErrorResult("checklist_id and name are required"), nil
	}
	body := url.Values{"name": {name}}
	if v, ok := args["checked"].(bool); ok && v {
		body.Set("checked", "true")
	}
	if v := mcpserver.StrArg(args, "pos", ""); v != "" {
		body.Set("pos", v)
	}
	return s.post(ctx, fmt.Sprintf("/checklists/%s/checkItems", clID), body)
}

func (s *Server) handleUpdateChecklistItem(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	itemID := mcpserver.StrArg(args, "checklist_item_id", "")
	if cardID == "" || itemID == "" {
		return mcpserver.ErrorResult("card_id and checklist_item_id are required"), nil
	}
	body := url.Values{}
	if v := mcpserver.StrArg(args, "state", ""); v != "" {
		body.Set("state", v)
	}
	if v := mcpserver.StrArg(args, "name", ""); v != "" {
		body.Set("name", v)
	}
	if len(body) == 0 {
		return mcpserver.ErrorResult("no fields to update"), nil
	}
	return s.put(ctx, fmt.Sprintf("/cards/%s/checkItem/%s", cardID, itemID), body)
}

func (s *Server) handleDeleteChecklist(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "checklist_id", "")
	if id == "" {
		return mcpserver.ErrorResult("checklist_id is required"), nil
	}
	return s.delete(ctx, fmt.Sprintf("/checklists/%s", id))
}

func (s *Server) handleAddComment(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	text := mcpserver.StrArg(args, "text", "")
	if cardID == "" || text == "" {
		return mcpserver.ErrorResult("card_id and text are required"), nil
	}
	return s.post(ctx, fmt.Sprintf("/cards/%s/actions/comments", cardID), url.Values{"text": {text}})
}

func (s *Server) handleDeleteComment(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	actionID := mcpserver.StrArg(args, "action_id", "")
	if cardID == "" || actionID == "" {
		return mcpserver.ErrorResult("card_id and action_id are required"), nil
	}
	return s.delete(ctx, fmt.Sprintf("/cards/%s/actions/%s/comments", cardID, actionID))
}

func (s *Server) handleAddLabelToCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	labelID := mcpserver.StrArg(args, "label_id", "")
	if cardID == "" || labelID == "" {
		return mcpserver.ErrorResult("card_id and label_id are required"), nil
	}
	return s.post(ctx, fmt.Sprintf("/cards/%s/idLabels", cardID), url.Values{"value": {labelID}})
}

func (s *Server) handleRemoveLabelFromCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	labelID := mcpserver.StrArg(args, "label_id", "")
	if cardID == "" || labelID == "" {
		return mcpserver.ErrorResult("card_id and label_id are required"), nil
	}
	return s.delete(ctx, fmt.Sprintf("/cards/%s/idLabels/%s", cardID, labelID))
}

func (s *Server) handleCreateLabel(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	boardID := mcpserver.StrArg(args, "board_id", "")
	name := mcpserver.StrArg(args, "name", "")
	if boardID == "" || name == "" {
		return mcpserver.ErrorResult("board_id and name are required"), nil
	}
	if r := s.checkBoard(boardID); r != nil {
		return r, nil
	}
	body := url.Values{"idBoard": {boardID}, "name": {name}}
	if v := mcpserver.StrArg(args, "color", ""); v != "" {
		body.Set("color", v)
	}
	return s.post(ctx, "/labels", body)
}

func (s *Server) handleAssignMemberToCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	memberID := mcpserver.StrArg(args, "member_id", "")
	if cardID == "" || memberID == "" {
		return mcpserver.ErrorResult("card_id and member_id are required"), nil
	}
	return s.post(ctx, fmt.Sprintf("/cards/%s/idMembers", cardID), url.Values{"value": {memberID}})
}

func (s *Server) handleRemoveMemberFromCard(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	memberID := mcpserver.StrArg(args, "member_id", "")
	if cardID == "" || memberID == "" {
		return mcpserver.ErrorResult("card_id and member_id are required"), nil
	}
	return s.delete(ctx, fmt.Sprintf("/cards/%s/idMembers/%s", cardID, memberID))
}

func (s *Server) handleAddURLAttachment(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	cardID := mcpserver.StrArg(args, "card_id", "")
	rawURL := mcpserver.StrArg(args, "url", "")
	if cardID == "" || rawURL == "" {
		return mcpserver.ErrorResult("card_id and url are required"), nil
	}
	body := url.Values{"url": {rawURL}}
	if v := mcpserver.StrArg(args, "name", ""); v != "" {
		body.Set("name", v)
	}
	return s.post(ctx, fmt.Sprintf("/cards/%s/attachments", cardID), body)
}

func (s *Server) handleCreateList(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	boardID := mcpserver.StrArg(args, "board_id", "")
	name := mcpserver.StrArg(args, "name", "")
	if boardID == "" || name == "" {
		return mcpserver.ErrorResult("board_id and name are required"), nil
	}
	if r := s.checkBoard(boardID); r != nil {
		return r, nil
	}
	body := url.Values{"idBoard": {boardID}, "name": {name}}
	if v := mcpserver.StrArg(args, "pos", ""); v != "" {
		body.Set("pos", v)
	}
	return s.post(ctx, "/lists", body)
}

func (s *Server) handleArchiveList(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	id := mcpserver.StrArg(args, "list_id", "")
	if id == "" {
		return mcpserver.ErrorResult("list_id is required"), nil
	}
	return s.put(ctx, fmt.Sprintf("/lists/%s/closed", id), url.Values{"value": {"true"}})
}

// ── HTTP helpers ───────────────────────────────────────────────────────────

func (s *Server) authParams() url.Values {
	return url.Values{
		"key":   {s.cfg.APIKey},
		"token": {s.cfg.Token},
	}
}

func (s *Server) buildURL(path string, extra url.Values) string {
	q := s.authParams()
	for k, vs := range extra {
		q[k] = vs
	}
	return baseURL + path + "?" + q.Encode()
}

func (s *Server) get(ctx context.Context, path string, params url.Values) (*mcpserver.CallToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.buildURL(path, params), nil)
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	return s.do(req)
}

func (s *Server) post(ctx context.Context, path string, body url.Values) (*mcpserver.CallToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.buildURL(path, nil),
		strings.NewReader(body.Encode()))
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

func (s *Server) put(ctx context.Context, path string, body url.Values) (*mcpserver.CallToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.buildURL(path, nil),
		strings.NewReader(body.Encode()))
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

func (s *Server) delete(ctx context.Context, path string) (*mcpserver.CallToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.buildURL(path, nil), nil)
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	return s.do(req)
}

func (s *Server) do(req *http.Request) (*mcpserver.CallToolResult, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("read response failed: %v", err)), nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpserver.ErrorResult(fmt.Sprintf("Trello API error %d: %s", resp.StatusCode, string(data))), nil
	}

	// Pretty-print JSON for readability
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err == nil {
		return mcpserver.TextResult(pretty.String()), nil
	}
	return mcpserver.TextResult(string(data)), nil
}

// ── Board whitelist ────────────────────────────────────────────────────────

// checkBoard returns an error result if board access is not allowed.
// Returns nil when the board is permitted (or no whitelist is configured).
func (s *Server) checkBoard(boardID string) *mcpserver.CallToolResult {
	if s.allowedBoards == nil {
		return nil
	}
	if !s.allowedBoards[boardID] {
		return mcpserver.ErrorResult(fmt.Sprintf("board %s is not in the allowed boards list", boardID))
	}
	return nil
}

// ── Shared helpers ─────────────────────────────────────────────────────────



func mustMarshal(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

