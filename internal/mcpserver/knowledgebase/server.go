package knowledgebase

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Server implements an MCP server for semantic + keyword search over a
// directory of Markdown documents.
type Server struct {
	mcpserver.BaseServer
	rootDir  string
	index    *Index
	indexer  *Indexer
	searcher *searcher
	embedder Embedder
	handlers map[string]mcpserver.ToolHandler
}

// Config holds configuration for the knowledgebase server.
type Config struct {
	// RootDir is the directory of Markdown documents to index (required).
	RootDir string
	// IndexPath is where the persistent index is stored.
	// Defaults to <RootDir>/.kb-index.json.
	IndexPath string
	// EmbedURL is the base URL of an OpenAI-compatible embeddings API.
	// Leave empty to use BM25-only search (no embedding).
	EmbedURL string
	// EmbedModel is the model name passed to the embedding API.
	// Defaults to "nomic-embed-text".
	EmbedModel string
	// EmbedAPIKey is the API key for the embedding endpoint (optional).
	EmbedAPIKey string
	// BM25Weight controls the blend of BM25 vs. semantic scores.
	// 0.0 = pure semantic, 1.0 = pure BM25. Default: 0.3.
	BM25Weight float64
	// TopK is the default number of search results to return. Default: 5.
	TopK int

	Name    string
	Version string
}

func (c *Config) setDefaults() {
	if c.IndexPath == "" {
		c.IndexPath = filepath.Join(c.RootDir, ".kb-index.json")
	}
	if c.BM25Weight == 0 {
		c.BM25Weight = 0.3
	}
	if c.TopK == 0 {
		c.TopK = 5
	}
}

// NewServer creates and initialises a knowledgebase MCP server.
// It performs an incremental sync of the index on startup.
func NewServer(ctx context.Context, cfg Config) (*Server, error) {
	cfg.setDefaults()

	absRoot, err := filepath.Abs(cfg.RootDir)
	if err != nil {
		return nil, fmt.Errorf("invalid root dir: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("root dir not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path is not a directory: %s", absRoot)
	}

	idx, err := loadIndex(cfg.IndexPath)
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}

	var embedder Embedder
	if cfg.EmbedURL != "" {
		embedder = NewHTTPEmbedder(cfg.EmbedURL, cfg.EmbedModel, cfg.EmbedAPIKey)
		log.Printf("knowledgebase: embedding via %s (model: %s)", cfg.EmbedURL, cfg.EmbedModel)
	} else {
		embedder = &noopEmbedder{}
		log.Printf("knowledgebase: no embed URL configured — using BM25-only search")
	}

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: cfg.Name, Version: cfg.Version},
		rootDir:    absRoot,
		index:      idx,
		embedder:   embedder,
		handlers:   make(map[string]mcpserver.ToolHandler),
	}
	s.indexer = newIndexer(absRoot, idx, embedder)
	s.searcher = newSearcher(idx)

	// Synchronise index on startup
	if err := s.indexer.Sync(ctx); err != nil {
		log.Printf("knowledgebase: sync warning: %v", err)
	}
	if err := idx.save(); err != nil {
		log.Printf("knowledgebase: save index warning: %v", err)
	}

	s.handlers["kb_search"] = s.handleSearch(cfg.BM25Weight, cfg.TopK)
	s.handlers["kb_list"] = s.handleList
	s.handlers["kb_get"] = s.handleGet
	s.handlers["kb_reindex"] = s.handleReindex

	return s, nil
}

// ListTools returns all available tools.
func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "kb_search",
			Description: fmt.Sprintf("Search the knowledgebase at %s using hybrid BM25 + semantic search. Returns relevant document chunks with source paths and heading context.", s.rootDir),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": "Number of results to return (default: 5, max: 20)",
					},
					"bm25_weight": map[string]interface{}{
						"type":        "number",
						"description": "Weight of BM25 keyword score vs semantic score (0.0–1.0, default: server default). Higher values favour keyword matching.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "kb_list",
			Description: "List all documents in the knowledgebase with their chunk counts.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "kb_get",
			Description: "Retrieve the full content of a specific document from the knowledgebase by its relative path.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Relative path to the document (as returned by kb_list or kb_search)",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "kb_reindex",
			Description: "Re-scan the knowledgebase directory and update the index for any changed or new documents.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}, nil
}

// CallTool dispatches a tool call by name.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	h, ok := s.handlers[name]
	if !ok {
		return &mcpserver.CallToolResult{
			Content: []mcpserver.ContentBlock{{Type: "text", Text: fmt.Sprintf("unknown tool: %s", name)}},
			IsError: true,
		}, nil
	}
	return h(ctx, arguments)
}

// Shutdown is a no-op for this server.
func (s *Server) Shutdown(_ context.Context) error { return nil }

// --- Tool handlers ---

func (s *Server) handleSearch(defaultBM25Weight float64, defaultTopK int) mcpserver.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return errResult("query is required"), nil
		}

		topK := defaultTopK
		if v, ok := args["top_k"].(float64); ok && v > 0 {
			topK = int(v)
			if topK > 20 {
				topK = 20
			}
		}

		bm25Weight := defaultBM25Weight
		if v, ok := args["bm25_weight"].(float64); ok && v >= 0 && v <= 1 {
			bm25Weight = v
		}

		// Embed the query if we have a real embedder
		var queryVec []float32
		if _, isNoop := s.embedder.(*noopEmbedder); !isNoop {
			vecs, err := s.embedder.Embed(ctx, []string{query})
			if err != nil {
				log.Printf("knowledgebase: embed query: %v", err)
				// Fall back to BM25-only
				bm25Weight = 1.0
			} else if len(vecs) > 0 {
				queryVec = vecs[0]
			}
		}

		results := s.searcher.Search(query, queryVec, topK, bm25Weight)
		if len(results) == 0 {
			return textResult("No results found."), nil
		}

		var sb strings.Builder
		for i, r := range results {
			fmt.Fprintf(&sb, "### Result %d — %s\n", i+1, r.Chunk.DocPath)
			if r.Chunk.Heading != "" {
				fmt.Fprintf(&sb, "**Section:** %s\n", r.Chunk.Heading)
			}
			fmt.Fprintf(&sb, "**Score:** %.3f (bm25=%.3f, cosine=%.3f)\n\n", r.Score, r.BM25Score, r.CosineScore)
			fmt.Fprintf(&sb, "%s\n\n---\n\n", r.Chunk.Content)
		}

		return textResult(sb.String()), nil
	}
}

func (s *Server) handleList(_ context.Context, _ map[string]interface{}) (*mcpserver.CallToolResult, error) {
	chunks := s.index.chunks()
	if len(chunks) == 0 {
		return textResult("No documents indexed."), nil
	}

	// Aggregate chunk counts per document
	counts := make(map[string]int)
	order := make([]string, 0)
	seen := make(map[string]bool)
	for _, c := range chunks {
		counts[c.DocPath]++
		if !seen[c.DocPath] {
			order = append(order, c.DocPath)
			seen[c.DocPath] = true
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**%d documents indexed** (%d total chunks)\n\n", len(order), len(chunks))
	for _, p := range order {
		fmt.Fprintf(&sb, "- `%s` (%d chunks)\n", p, counts[p])
	}
	return textResult(sb.String()), nil
}

func (s *Server) handleGet(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	relPath, _ := args["path"].(string)
	if relPath == "" {
		return errResult("path is required"), nil
	}
	// Prevent path traversal
	clean := filepath.Clean(relPath)
	if strings.HasPrefix(clean, "..") {
		return errResult("invalid path"), nil
	}
	absPath := filepath.Join(s.rootDir, clean)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errResult(fmt.Sprintf("document not found: %s", relPath)), nil
		}
		return errResult(fmt.Sprintf("read error: %v", err)), nil
	}
	const maxBytes = 100 * 1024
	if len(data) > maxBytes {
		data = data[:maxBytes]
		return textResult(fmt.Sprintf("# %s\n\n%s\n\n*(truncated to 100KB)*", relPath, data)), nil
	}
	return textResult(fmt.Sprintf("# %s\n\n%s", relPath, data)), nil
}

func (s *Server) handleReindex(ctx context.Context, _ map[string]interface{}) (*mcpserver.CallToolResult, error) {
	log.Printf("knowledgebase: reindex requested")
	if err := s.indexer.Sync(ctx); err != nil {
		return errResult(fmt.Sprintf("reindex failed: %v", err)), nil
	}
	if err := s.index.save(); err != nil {
		return errResult(fmt.Sprintf("save index: %v", err)), nil
	}
	chunks := s.index.chunks()
	docs := s.index.docPaths()
	return textResult(fmt.Sprintf("Reindex complete. %d documents, %d chunks.", len(docs), len(chunks))), nil
}

// --- Helpers ---

func textResult(text string) *mcpserver.CallToolResult {
	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{{Type: "text", Text: text}},
	}
}

func errResult(text string) *mcpserver.CallToolResult {
	return &mcpserver.CallToolResult{
		Content: []mcpserver.ContentBlock{{Type: "text", Text: text}},
		IsError: true,
	}
}
