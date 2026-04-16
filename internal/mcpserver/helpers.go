package mcpserver

// TextResult returns a successful CallToolResult containing a plain text message.
func TextResult(text string) *CallToolResult {
	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

// ErrorResult returns an error CallToolResult containing a plain text message.
func ErrorResult(msg string) *CallToolResult {
	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: msg}},
		IsError: true,
	}
}
