package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stanier/stoa/internal/skills"
)

// MCPToolAdapter adapts an MCP tool to the skills.Tool interface
type MCPToolAdapter struct {
	client      *Client
	serverName  string
	description ToolDescription
}

// NewMCPToolAdapter creates a new MCP tool adapter
func NewMCPToolAdapter(client *Client, serverName string, desc ToolDescription) *MCPToolAdapter {
	return &MCPToolAdapter{
		client:      client,
		serverName:  serverName,
		description: desc,
	}
}

// Name returns the tool name
func (a *MCPToolAdapter) Name() string {
	return fmt.Sprintf("mcp_%s_%s", a.serverName, a.description.Name)
}

// ServerName returns the name of the MCP server that provides this tool
func (a *MCPToolAdapter) ServerName() string {
	return a.serverName
}

// Description returns the tool description
func (a *MCPToolAdapter) Description() string {
	return a.description.Description
}

// Schema returns the tool input schema
func (a *MCPToolAdapter) Schema() skills.ToolSchema {
	// Extract properties from the MCP schema
	properties := make(map[string]interface{})
	if props, ok := a.description.InputSchema["properties"].(map[string]interface{}); ok {
		properties = props
	}

	return skills.ToolSchema{
		Type:       "object",
		Properties: properties,
		Required:   extractRequired(a.description.InputSchema),
	}
}

// Execute executes the tool via MCP
func (a *MCPToolAdapter) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	// Parse parameters
	var args map[string]interface{}
	if err := json.Unmarshal(params, &args); err != nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Invalid parameters: %v", err),
		}, nil
	}

	// Call MCP tool
	result, err := a.client.CallTool(ctx, CallToolParams{
		Name:      a.description.Name,
		Arguments: args,
	})

	if err != nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("MCP call failed: %v", err),
		}, nil
	}

	// Check for tool error
	if result.IsError {
		errorText := extractTextContent(result.Content)
		return skills.ToolResult{
			Success: false,
			Error:   errorText,
		}, nil
	}

	// Extract text content from result
	content := extractTextContent(result.Content)

	return skills.ToolResult{
		Success: true,
		Content: content,
	}, nil
}

// extractTextContent extracts text from content blocks
func extractTextContent(blocks []ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == "text" {
			result += block.Text
		}
	}
	return result
}

// extractRequired extracts required fields from JSON Schema
func extractRequired(schema map[string]interface{}) []string {
	if required, ok := schema["required"].([]interface{}); ok {
		result := make([]string, 0, len(required))
		for _, r := range required {
			if str, ok := r.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}
