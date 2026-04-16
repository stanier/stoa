package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Entry represents a single memory entry.
type Entry struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	Tags      []string   `json:"tags,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// isExpired returns true if the entry has passed its TTL.
func (e *Entry) isExpired() bool {
	if e.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*e.ExpiresAt)
}

// storageData is the top-level JSON structure persisted to disk.
type storageData struct {
	Entries map[string]*Entry `json:"entries"`
	Version int               `json:"version"`
}

// Server implements an MCP server for persistent key-value memory storage.
// Entries are stored in a JSON file with atomic writes. Supports TTL, tags,
// and full-text search over keys and values.
type Server struct {
	mcpserver.BaseServer
	storagePath string
	maxEntries  int
	handlers    map[string]mcpserver.ToolHandler
	mu          sync.RWMutex
	data        *storageData
}

// NewServer creates a new memory MCP server. If storagePath is empty, defaults
// to ~/.config/stoa/memory.json. maxEntries of 0 means unlimited.
func NewServer(storagePath, name, version string, maxEntries int) (*Server, error) {
	if storagePath == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			configDir = "."
		}
		storagePath = filepath.Join(configDir, "stoa", "memory.json")
	}

	s := &Server{
		BaseServer:  mcpserver.BaseServer{Name: name, Version: version},
		storagePath: storagePath,
		maxEntries:  maxEntries,
		handlers:    make(map[string]mcpserver.ToolHandler),
		data:        &storageData{Entries: make(map[string]*Entry), Version: 1},
	}

	s.handlers["memory_set"] = s.handleSet
	s.handlers["memory_get"] = s.handleGet
	s.handlers["memory_delete"] = s.handleDelete
	s.handlers["memory_list"] = s.handleList
	s.handlers["memory_search"] = s.handleSearch
	s.handlers["memory_stats"] = s.handleStats

	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load storage from %s: %w", storagePath, err)
	}

	// Prune expired entries on startup (no concurrent access yet, no lock needed).
	s.pruneExpiredLocked()

	return s, nil
}


// ListTools returns all available memory tools.
func (s *Server) ListTools(ctx context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "memory_set",
			Description: "Store a key-value pair in persistent memory. Overwrites if key already exists.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "Unique key for this memory entry",
					},
					"value": map[string]interface{}{
						"type":        "string",
						"description": "Value to store (any text, including JSON)",
					},
					"tags": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Optional tags for categorization (e.g. [\"project:foo\", \"type:config\"])",
					},
					"ttl_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Optional time-to-live in seconds. Entry is automatically removed after this duration.",
					},
				},
				"required": []string{"key", "value"},
			},
		},
		{
			Name:        "memory_get",
			Description: "Retrieve a memory entry by its exact key.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "Key to retrieve",
					},
				},
				"required": []string{"key"},
			},
		},
		{
			Name:        "memory_delete",
			Description: "Delete a memory entry by its exact key.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"key": map[string]interface{}{
						"type":        "string",
						"description": "Key to delete",
					},
				},
				"required": []string{"key"},
			},
		},
		{
			Name:        "memory_list",
			Description: "List memory entries, optionally filtered by tag. Returns entries sorted by most recently updated.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tag": map[string]interface{}{
						"type":        "string",
						"description": "Optional tag to filter by (exact match)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of entries to return (default: 100)",
					},
				},
			},
		},
		{
			Name:        "memory_search",
			Description: "Search memory entries by query string. Searches keys (weighted 2x), values, and tags. Returns results sorted by relevance.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Case-insensitive search query",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results (default: 20)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "memory_stats",
			Description: "Get statistics about the memory store: entry count, capacity, storage file location and size, and tag distribution.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// CallTool dispatches a tool call to the appropriate handler.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", name)}},
			IsError: true,
		}, nil
	}
	return handler(ctx, arguments)
}

// Shutdown prunes expired entries and persists the final state.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked()
	return s.save()
}

// --- Tool handlers ---

func (s *Server) handleSet(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	key, ok := arguments["key"].(string)
	if !ok || key == "" {
		return mcpserver.ErrorResult("key is required and must be a non-empty string"), nil
	}
	value, ok := arguments["value"].(string)
	if !ok {
		return mcpserver.ErrorResult("value is required"), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce capacity limit for new entries.
	_, exists := s.data.Entries[key]
	if !exists && s.maxEntries > 0 && len(s.data.Entries) >= s.maxEntries {
		return mcpserver.ErrorResult(fmt.Sprintf("memory store is full (%d/%d entries); delete some entries first", len(s.data.Entries), s.maxEntries)), nil
	}

	now := time.Now()
	entry := &Entry{
		Key:       key,
		Value:     value,
		UpdatedAt: now,
	}

	// Parse tags.
	if raw, ok := arguments["tags"]; ok {
		if tagList, ok := raw.([]interface{}); ok {
			for _, t := range tagList {
				if tag, ok := t.(string); ok && tag != "" {
					entry.Tags = append(entry.Tags, tag)
				}
			}
		}
	}

	// Parse TTL.
	if raw, ok := arguments["ttl_seconds"]; ok {
		ttlSecs := toInt(raw)
		if ttlSecs > 0 {
			expiry := now.Add(time.Duration(ttlSecs) * time.Second)
			entry.ExpiresAt = &expiry
		}
	}

	// Preserve created_at when updating an existing key.
	if existing, ok := s.data.Entries[key]; ok {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = now
	}

	s.data.Entries[key] = entry

	if err := s.save(); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("entry stored in memory but failed to persist to disk: %v", err)), nil
	}

	msg := fmt.Sprintf("Stored: %s", key)
	if entry.ExpiresAt != nil {
		msg += fmt.Sprintf(" (expires %s)", entry.ExpiresAt.Format(time.RFC3339))
	}
	return mcpserver.TextResult(msg), nil
}

func (s *Server) handleGet(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	key, ok := arguments["key"].(string)
	if !ok || key == "" {
		return mcpserver.ErrorResult("key is required"), nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.data.Entries[key]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("key not found: %s", key)), nil
	}
	if entry.isExpired() {
		return mcpserver.ErrorResult(fmt.Sprintf("key has expired: %s", key)), nil
	}

	return mcpserver.TextResult(formatEntry(entry)), nil
}

func (s *Server) handleDelete(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	key, ok := arguments["key"].(string)
	if !ok || key == "" {
		return mcpserver.ErrorResult("key is required"), nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data.Entries[key]; !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("key not found: %s", key)), nil
	}

	delete(s.data.Entries, key)

	if err := s.save(); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("deleted from memory but failed to persist: %v", err)), nil
	}

	return mcpserver.TextResult(fmt.Sprintf("Deleted: %s", key)), nil
}

func (s *Server) handleList(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	filterTag, _ := arguments["tag"].(string)
	limit := 100
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var entries []*Entry
	for _, entry := range s.data.Entries {
		if entry.isExpired() {
			continue
		}
		if filterTag != "" && !hasTag(entry, filterTag) {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	if len(entries) == 0 {
		if filterTag != "" {
			return mcpserver.TextResult(fmt.Sprintf("No entries with tag: %s", filterTag)), nil
		}
		return mcpserver.TextResult("No entries in memory."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d entries:\n\n", len(entries)))
	for _, entry := range entries {
		sb.WriteString(formatEntryBrief(entry))
		sb.WriteString("\n")
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleSearch(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := arguments["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}
	queryLower := strings.ToLower(query)

	limit := 20
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	type scoredEntry struct {
		entry *Entry
		score int
	}

	var results []scoredEntry
	for _, entry := range s.data.Entries {
		if entry.isExpired() {
			continue
		}
		score := 0
		if strings.Contains(strings.ToLower(entry.Key), queryLower) {
			score += 2 // Key matches are weighted higher — more intentional
		}
		if strings.Contains(strings.ToLower(entry.Value), queryLower) {
			score++
		}
		for _, tag := range entry.Tags {
			if strings.Contains(strings.ToLower(tag), queryLower) {
				score++
			}
		}
		if score > 0 {
			results = append(results, scoredEntry{entry, score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].entry.UpdatedAt.After(results[j].entry.UpdatedAt)
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	if len(results) == 0 {
		return mcpserver.TextResult(fmt.Sprintf("No results for query: %q", query)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results for %q:\n\n", len(results), query))
	for _, r := range results {
		sb.WriteString(formatEntry(r.entry))
		sb.WriteString("---\n")
	}
	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleStats(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.data.Entries)
	expired := 0
	tagCounts := make(map[string]int)

	for _, entry := range s.data.Entries {
		if entry.isExpired() {
			expired++
		}
		for _, tag := range entry.Tags {
			tagCounts[tag]++
		}
	}

	fileSize := int64(-1)
	if info, err := os.Stat(s.storagePath); err == nil {
		fileSize = info.Size()
	}

	var sb strings.Builder
	sb.WriteString("Memory Store Statistics\n")
	sb.WriteString("=======================\n")
	sb.WriteString(fmt.Sprintf("Active entries:  %d\n", total-expired))
	sb.WriteString(fmt.Sprintf("Expired entries: %d\n", expired))
	sb.WriteString(fmt.Sprintf("Total entries:   %d\n", total))
	if s.maxEntries > 0 {
		sb.WriteString(fmt.Sprintf("Capacity:        %d / %d\n", total-expired, s.maxEntries))
	} else {
		sb.WriteString("Capacity:        unlimited\n")
	}
	sb.WriteString(fmt.Sprintf("Storage file:    %s\n", s.storagePath))
	if fileSize >= 0 {
		sb.WriteString(fmt.Sprintf("Storage size:    %d bytes\n", fileSize))
	}
	if len(tagCounts) > 0 {
		// Sort tags for deterministic output.
		tags := make([]string, 0, len(tagCounts))
		for t := range tagCounts {
			tags = append(tags, t)
		}
		sort.Strings(tags)
		sb.WriteString("Tags:\n")
		for _, tag := range tags {
			sb.WriteString(fmt.Sprintf("  %-30s %d entries\n", tag, tagCounts[tag]))
		}
	}

	return mcpserver.TextResult(sb.String()), nil
}

// --- Storage helpers ---

// load reads the JSON storage file into s.data. Returns os.ErrNotExist if the
// file doesn't exist yet (first run).
func (s *Server) load() error {
	raw, err := os.ReadFile(s.storagePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, s.data)
}

// save atomically writes s.data to the storage file via a temp file + rename.
// Caller must hold s.mu (write lock).
func (s *Server) save() error {
	if err := os.MkdirAll(filepath.Dir(s.storagePath), 0755); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal storage: %w", err)
	}

	tmp := s.storagePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, s.storagePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// pruneExpiredLocked removes expired entries. Caller must hold s.mu (write lock)
// or call before any concurrent access.
func (s *Server) pruneExpiredLocked() {
	for key, entry := range s.data.Entries {
		if entry.isExpired() {
			delete(s.data.Entries, key)
		}
	}
}

// --- Formatting helpers ---

func formatEntry(e *Entry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Key:     %s\n", e.Key))
	sb.WriteString(fmt.Sprintf("Value:   %s\n", e.Value))
	if len(e.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Tags:    %s\n", strings.Join(e.Tags, ", ")))
	}
	sb.WriteString(fmt.Sprintf("Created: %s\n", e.CreatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Updated: %s\n", e.UpdatedAt.Format(time.RFC3339)))
	if e.ExpiresAt != nil {
		sb.WriteString(fmt.Sprintf("Expires: %s\n", e.ExpiresAt.Format(time.RFC3339)))
	}
	return sb.String()
}

func formatEntryBrief(e *Entry) string {
	preview := e.Value
	if len(preview) > 120 {
		preview = preview[:120] + "..."
	}
	tags := ""
	if len(e.Tags) > 0 {
		tags = fmt.Sprintf(" [%s]", strings.Join(e.Tags, ", "))
	}
	return fmt.Sprintf("%s%s: %s", e.Key, tags, preview)
}



func hasTag(e *Entry, tag string) bool {
	for _, t := range e.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// toInt converts a JSON-decoded number (float64) or int to int.
func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
