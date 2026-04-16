package filesystem

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Server implements an MCP server for filesystem operations
type Server struct {
	mcpserver.BaseServer
	rootDir  string
	handlers map[string]mcpserver.ToolHandler
}

// NewServer creates a new filesystem MCP server
func NewServer(rootDir, name, version string) (*Server, error) {
	// Validate and resolve root directory
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("invalid root directory: %w", err)
	}

	info, err := os.Stat(absRoot)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(absRoot, 0755); mkErr != nil {
			return nil, fmt.Errorf("root directory does not exist and could not be created: %w", mkErr)
		}
		info, err = os.Stat(absRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("root directory not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path is not a directory: %s", absRoot)
	}

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		rootDir:    absRoot,
		handlers:   make(map[string]mcpserver.ToolHandler),
	}

	// Register tool handlers
	s.handlers["read_file"] = s.handleReadFile
	s.handlers["write_file"] = s.handleWriteFile
	s.handlers["list_directory"] = s.handleListDirectory
	s.handlers["search_files"] = s.handleSearchFiles
	s.handlers["search_content"] = s.handleSearchContent

	return s, nil
}

// ListTools returns all available tools
func (s *Server) ListTools(ctx context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "read_file",
			Description: fmt.Sprintf("Read the contents of a file within %s", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file (relative to root directory)",
					},
					"max_bytes": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum bytes to read (default: 512KB). Use to avoid flooding context with large files.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: fmt.Sprintf("Write content to a file within %s", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file (relative to root directory)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "list_directory",
			Description: fmt.Sprintf("List contents of a directory within %s", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the directory (relative to root directory, empty for root)",
					},
				},
			},
		},
		{
			Name:        "search_files",
			Description: fmt.Sprintf("Search for files by name pattern within %s", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Filename pattern to search for (supports wildcards)",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to search in (relative to root directory, empty for root)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "search_content",
			Description: fmt.Sprintf("Search for text content within files in %s. Returns matching lines with file path and line number.", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Text to search for within file contents",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to search in (relative to root directory, empty for root)",
					},
					"file_pattern": map[string]interface{}{
						"type":        "string",
						"description": "Optional filename pattern to restrict which files are searched (e.g. '*.go', '*.txt')",
					},
					"case_sensitive": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the search is case-sensitive (default: false)",
					},
					"context_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Number of lines of context to include before and after each match (default: 0)",
					},
				},
				"required": []string{"query"},
			},
		},
	}, nil
}

// CallTool executes a tool by name
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", name)},
			},
			IsError: true,
		}, nil
	}

	return handler(ctx, arguments)
}

// Shutdown cleans up resources
func (s *Server) Shutdown(ctx context.Context) error {
	return nil
}

// validatePath ensures the path is within the root directory.
// Accepts both absolute paths (within rootDir) and relative paths.
// Symlinks are resolved before the root-containment check to prevent
// a symlink inside the root from pointing outside it.
func (s *Server) validatePath(relPath string) (string, error) {
	var absPath string
	if filepath.IsAbs(relPath) {
		absPath = filepath.Clean(relPath)
	} else {
		absPath = filepath.Clean(filepath.Join(s.rootDir, relPath))
	}

	// Ensure it's within root — add trailing separator to avoid /tmp matching /tmp2
	rootWithSep := s.rootDir + string(filepath.Separator)
	if absPath != s.rootDir && !strings.HasPrefix(absPath, rootWithSep) {
		return "", fmt.Errorf("path escapes root directory: %s", relPath)
	}

	// Resolve symlinks and re-validate to prevent symlink escape attacks.
	// EvalSymlinks returns an error if the path doesn't exist yet (e.g. for
	// writes to new files), so only apply it when the path already exists.
	if _, err := os.Lstat(absPath); err == nil {
		resolved, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve path %s: %w", relPath, err)
		}
		if resolved != s.rootDir && !strings.HasPrefix(resolved, rootWithSep) {
			return "", fmt.Errorf("path escapes root directory via symlink: %s", relPath)
		}
		absPath = resolved
	}

	return absPath, nil
}

// handleReadFile reads a file
func (s *Server) handleReadFile(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	pathRaw, ok := arguments["path"]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Missing required parameter: path"},
			},
			IsError: true,
		}, nil
	}

	path, ok := pathRaw.(string)
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Parameter 'path' must be a string"},
			},
			IsError: true,
		}, nil
	}

	absPath, err := s.validatePath(path)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}, nil
	}

	const defaultMaxBytes = 512 * 1024
	maxBytes := int64(defaultMaxBytes)
	if raw, ok := arguments["max_bytes"]; ok {
		switch v := raw.(type) {
		case float64:
			if v > 0 {
				maxBytes = int64(v)
			}
		case int:
			if v > 0 {
				maxBytes = int64(v)
			}
		}
	}

	f, err := os.Open(absPath)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error reading file: %v", err)},
			},
			IsError: true,
		}, nil
	}
	defer f.Close()

	// Read up to maxBytes+1 so we can detect truncation.
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error reading file: %v", err)},
			},
			IsError: true,
		}, nil
	}

	truncated := int64(len(raw)) > maxBytes
	content := raw
	if truncated {
		content = raw[:maxBytes]
	}

	text := string(content)
	if truncated {
		text += fmt.Sprintf("\n\n[truncated — file exceeds %d bytes; use max_bytes to read more]", maxBytes)
	}

	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{
			{Type: "text", Text: text},
		},
	}, nil
}

// handleWriteFile writes a file
func (s *Server) handleWriteFile(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	pathRaw, ok := arguments["path"]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Missing required parameter: path"},
			},
			IsError: true,
		}, nil
	}

	contentRaw, ok := arguments["content"]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Missing required parameter: content"},
			},
			IsError: true,
		}, nil
	}

	path, ok := pathRaw.(string)
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Parameter 'path' must be a string"},
			},
			IsError: true,
		}, nil
	}

	content, ok := contentRaw.(string)
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Parameter 'content' must be a string"},
			},
			IsError: true,
		}, nil
	}

	absPath, err := s.validatePath(path)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}, nil
	}

	// Create parent directories if needed
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error creating directory: %v", err)},
			},
			IsError: true,
		}, nil
	}

	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error writing file: %v", err)},
			},
			IsError: true,
		}, nil
	}

	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)},
		},
	}, nil
}

// handleListDirectory lists directory contents
func (s *Server) handleListDirectory(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	pathRaw, _ := arguments["path"]
	path := ""
	if pathRaw != nil {
		var ok bool
		path, ok = pathRaw.(string)
		if !ok {
			return &mcpserver.CallToolResult{
				Content: []mcpserver.ContentBlock{
					{Type: "text", Text: "Parameter 'path' must be a string"},
				},
				IsError: true,
			}, nil
		}
	}

	absPath, err := s.validatePath(path)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}, nil
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error reading directory: %v", err)},
			},
			IsError: true,
		}, nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Contents of %s:\n\n", absPath))

	for _, entry := range entries {
		typeStr := "file"
		if entry.IsDir() {
			typeStr = "dir "
		}

		info, err := entry.Info()
		size := int64(0)
		if err == nil {
			size = info.Size()
		}

		result.WriteString(fmt.Sprintf("[%s] %s (%d bytes)\n", typeStr, entry.Name(), size))
	}

	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{
			{Type: "text", Text: result.String()},
		},
	}, nil
}

// handleSearchFiles searches for files by pattern
func (s *Server) handleSearchFiles(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	patternRaw, ok := arguments["pattern"]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Missing required parameter: pattern"},
			},
			IsError: true,
		}, nil
	}

	pattern, ok := patternRaw.(string)
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Parameter 'pattern' must be a string"},
			},
			IsError: true,
		}, nil
	}

	pathRaw, _ := arguments["path"]
	searchPath := ""
	if pathRaw != nil {
		var ok bool
		searchPath, ok = pathRaw.(string)
		if !ok {
			return &mcpserver.CallToolResult{
				Content: []mcpserver.ContentBlock{
					{Type: "text", Text: "Parameter 'path' must be a string"},
				},
				IsError: true,
			}, nil
		}
	}

	absPath, err := s.validatePath(searchPath)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}, nil
	}

	var matches []string
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if d.IsDir() {
			return nil
		}

		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err != nil {
			return nil // Skip pattern errors
		}
		if matched {
			// Make relative to root
			relPath, _ := filepath.Rel(s.rootDir, path)
			matches = append(matches, relPath)
		}
		return nil
	})

	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error searching files: %v", err)},
			},
			IsError: true,
		}, nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d files matching '%s':\n\n", len(matches), pattern))
	for _, match := range matches {
		result.WriteString(fmt.Sprintf("- %s\n", match))
	}

	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{
			{Type: "text", Text: result.String()},
		},
	}, nil
}

// handleSearchContent searches file contents for a text query
func (s *Server) handleSearchContent(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	queryRaw, ok := arguments["query"]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Missing required parameter: query"},
			},
			IsError: true,
		}, nil
	}

	query, ok := queryRaw.(string)
	if !ok || query == "" {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: "Parameter 'query' must be a non-empty string"},
			},
			IsError: true,
		}, nil
	}

	pathRaw, _ := arguments["path"]
	searchPath := ""
	if pathRaw != nil {
		searchPath, _ = pathRaw.(string)
	}

	filePattern := ""
	if fp, ok := arguments["file_pattern"]; ok && fp != nil {
		filePattern, _ = fp.(string)
	}

	caseSensitive := false
	if cs, ok := arguments["case_sensitive"]; ok && cs != nil {
		caseSensitive, _ = cs.(bool)
	}

	contextLines := 0
	if cl, ok := arguments["context_lines"]; ok && cl != nil {
		switch v := cl.(type) {
		case float64:
			contextLines = int(v)
		case int:
			contextLines = v
		}
		if contextLines < 0 {
			contextLines = 0
		}
		if contextLines > 10 {
			contextLines = 10
		}
	}

	absPath, err := s.validatePath(searchPath)
	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}, nil
	}

	searchQuery := query
	if !caseSensitive {
		searchQuery = strings.ToLower(query)
	}

	type match struct {
		file    string
		lineNum int
		line    string
		before  []string
		after   []string
	}

	const maxMatches = 200
	var allMatches []match
	totalMatchCount := 0

	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		if filePattern != "" {
			matched, err := filepath.Match(filePattern, filepath.Base(path))
			if err != nil || !matched {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Skip binary files (contains null bytes)
		if bytes.IndexByte(data, 0) != -1 {
			return nil
		}

		relPath, _ := filepath.Rel(s.rootDir, path)

		// Collect all lines for context
		var lines []string
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}

		for i, line := range lines {
			haystack := line
			if !caseSensitive {
				haystack = strings.ToLower(line)
			}
			if strings.Contains(haystack, searchQuery) {
				totalMatchCount++
				if len(allMatches) < maxMatches {
					m := match{
						file:    relPath,
						lineNum: i + 1,
						line:    line,
					}
					if contextLines > 0 {
						start := i - contextLines
						if start < 0 {
							start = 0
						}
						end := i + contextLines + 1
						if end > len(lines) {
							end = len(lines)
						}
						for j := start; j < i; j++ {
							m.before = append(m.before, lines[j])
						}
						for j := i + 1; j < end; j++ {
							m.after = append(m.after, lines[j])
						}
					}
					allMatches = append(allMatches, m)
				}
			}
		}

		return nil
	})

	if err != nil {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Error searching content: %v", err)},
			},
			IsError: true,
		}, nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d match(es) for %q", totalMatchCount, query))
	if totalMatchCount > maxMatches {
		result.WriteString(fmt.Sprintf(" (showing first %d)", maxMatches))
	}
	result.WriteString(":\n\n")

	lastFile := ""
	for _, m := range allMatches {
		if m.file != lastFile {
			if lastFile != "" {
				result.WriteString("\n")
			}
			result.WriteString(fmt.Sprintf("=== %s ===\n", m.file))
			lastFile = m.file
		}
		for j, bl := range m.before {
			result.WriteString(fmt.Sprintf("  %d: %s\n", m.lineNum-len(m.before)+j, bl))
		}
		result.WriteString(fmt.Sprintf("> %d: %s\n", m.lineNum, m.line))
		for j, al := range m.after {
			result.WriteString(fmt.Sprintf("  %d: %s\n", m.lineNum+1+j, al))
		}
	}

	if totalMatchCount == 0 {
		result.WriteString("No matches found.\n")
	}

	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{
			{Type: "text", Text: result.String()},
		},
	}, nil
}
