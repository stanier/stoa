package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/skills"
)

// PermissionAwareMCPTool wraps an MCP tool with permission checking
type PermissionAwareMCPTool struct {
	*MCPToolAdapter
	checker      permissions.Checker
	auditLogger  *permissions.AuditLogger
	serverName   string
	requestChan  chan<- *permissions.PermissionRequest
	mentionChan  chan<- *permissions.PermissionMention
}

// NewPermissionAwareMCPTool creates a new permission-aware MCP tool
func NewPermissionAwareMCPTool(
	adapter *MCPToolAdapter,
	checker permissions.Checker,
	auditLogger *permissions.AuditLogger,
	serverName string,
	requestChan chan<- *permissions.PermissionRequest,
	mentionChan chan<- *permissions.PermissionMention,
) *PermissionAwareMCPTool {
	return &PermissionAwareMCPTool{
		MCPToolAdapter: adapter,
		checker:        checker,
		auditLogger:    auditLogger,
		serverName:     serverName,
		requestChan:    requestChan,
		mentionChan:    mentionChan,
	}
}

// Execute executes the tool with permission checking
func (t *PermissionAwareMCPTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	// 1. Parse parameters
	var args map[string]interface{}
	if err := json.Unmarshal(params, &args); err != nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Invalid parameters: %v", err),
		}, nil
	}

	// 2. Check permissions
	decision, err := t.checker.Check(ctx, t.Name(), t.serverName, args)
	if err != nil {
		t.logAudit(false, args)
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Permission check failed: %v", err),
		}, nil
	}

	// 3. Handle based on rank
	switch decision.Rank {
	case permissions.RankSilent:
		// Execute immediately without notification
		t.logAudit(true, args)
		return t.MCPToolAdapter.Execute(ctx, params)

	case permissions.RankMention:
		// Execute and notify user
		result, err := t.MCPToolAdapter.Execute(ctx, params)
		t.logAudit(true, args)

		// Send mention notification (non-blocking)
		if t.mentionChan != nil {
			select {
			case t.mentionChan <- &permissions.PermissionMention{
				ToolName:   t.Name(),
				ServerName: t.serverName,
				Message:    decision.MentionText,
				Timestamp:  time.Now(),
			}:
			default:
				// Channel full, skip notification
			}
		}

		return result, err

	case permissions.RankRequest:
		// Request permission from user
		approved, err := t.requestPermission(ctx, decision, args)
		if err != nil {
			t.logAudit(false, args)
			return skills.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Permission request failed: %v", err),
			}, nil
		}

		if !approved {
			t.logAudit(false, args)
			return skills.ToolResult{
				Success: false,
				Error:   "Permission denied by user",
			}, nil
		}

		// Permission granted, execute
		t.logAudit(true, args)
		return t.MCPToolAdapter.Execute(ctx, params)

	default:
		t.logAudit(false, args)
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Permission rank %d (%s) not implemented", decision.Rank, decision.Rank.String()),
		}, nil
	}
}

// requestPermission requests permission from the user via UI
func (t *PermissionAwareMCPTool) requestPermission(
	ctx context.Context,
	decision *permissions.PermissionDecision,
	args map[string]interface{},
) (bool, error) {
	// Create permission request
	req := &permissions.PermissionRequest{
		ID:           generateRequestID(),
		ToolName:     t.Name(),
		ServerName:   t.serverName,
		Description:  t.Description(),
		Parameters:   args,
		Rank:         decision.Rank,
		RequestedAt:  time.Now(),
		ResponseChan: make(chan permissions.PermissionResponse, 1),
	}

	// Send to UI via channel
	select {
	case t.requestChan <- req:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	// Wait for user response
	select {
	case resp := <-req.ResponseChan:
		// Handle "remember choice"
		if resp.RememberChoice && resp.Approved {
			grant := permissions.PermissionGrant{
				ToolPattern: t.Name(), // Exact match for this tool
				ServerName:  t.serverName,
				Scope:       resp.Scope,
				GrantedAt:   time.Now(),
			}
			t.checker.GrantPermission(grant)
		}

		return resp.Approved, nil

	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// logAudit logs the permission decision to the audit log
func (t *PermissionAwareMCPTool) logAudit(allowed bool, params map[string]interface{}) {
	if t.auditLogger == nil {
		return
	}

	entry := permissions.AuditEntry{
		Timestamp:  time.Now(),
		ToolName:   t.Name(),
		ServerName: t.serverName,
		Decision:   allowed,
		Parameters: params,
	}

	// Log asynchronously to avoid blocking
	go t.auditLogger.Log(entry)
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
