package authdb

import (
	"context"
	"fmt"
)

// MCPPrefs holds a user's MCP tool preferences.
// Servers maps MCP server name → enabled.
// Tools maps exact tool name → enabled (overrides the server-level pref).
// Absent entries mean "enabled" (default).
type MCPPrefs struct {
	Servers map[string]bool `json:"servers"`
	Tools   map[string]bool `json:"tools"`
}

// GetMCPPrefs returns all MCP preferences for the given user.
// Both maps are always non-nil in the returned struct.
func (db *DB) GetMCPPrefs(ctx context.Context, userID string) (*MCPPrefs, error) {
	prefs := &MCPPrefs{
		Servers: make(map[string]bool),
		Tools:   make(map[string]bool),
	}

	rows, err := db.pool.Query(ctx,
		`SELECT server_name, enabled FROM user_mcp_server_prefs WHERE user_id=$1`, userID)
	if err != nil {
		return nil, fmt.Errorf("authdb: get mcp server prefs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var enabled bool
		if err := rows.Scan(&name, &enabled); err != nil {
			return nil, fmt.Errorf("authdb: scan mcp server pref: %w", err)
		}
		prefs.Servers[name] = enabled
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authdb: iterate mcp server prefs: %w", err)
	}

	rows2, err := db.pool.Query(ctx,
		`SELECT tool_name, enabled FROM user_mcp_tool_prefs WHERE user_id=$1`, userID)
	if err != nil {
		return nil, fmt.Errorf("authdb: get mcp tool prefs: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var name string
		var enabled bool
		if err := rows2.Scan(&name, &enabled); err != nil {
			return nil, fmt.Errorf("authdb: scan mcp tool pref: %w", err)
		}
		prefs.Tools[name] = enabled
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("authdb: iterate mcp tool prefs: %w", err)
	}

	return prefs, nil
}

// SetMCPServerPref upserts the enabled state for a given MCP server.
func (db *DB) SetMCPServerPref(ctx context.Context, userID, serverName string, enabled bool) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO user_mcp_server_prefs (user_id, server_name, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, server_name) DO UPDATE SET enabled = EXCLUDED.enabled`,
		userID, serverName, enabled)
	if err != nil {
		return fmt.Errorf("authdb: set mcp server pref: %w", err)
	}
	return nil
}

// SetMCPToolPref upserts the enabled state for a given MCP tool.
func (db *DB) SetMCPToolPref(ctx context.Context, userID, toolName string, enabled bool) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO user_mcp_tool_prefs (user_id, tool_name, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, tool_name) DO UPDATE SET enabled = EXCLUDED.enabled`,
		userID, toolName, enabled)
	if err != nil {
		return fmt.Errorf("authdb: set mcp tool pref: %w", err)
	}
	return nil
}
