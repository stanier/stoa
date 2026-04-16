package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

const (
	braveSearchURL    = "https://api.search.brave.com/res/v1/web/search"
	defaultMaxResults = 10
	defaultTimeoutSecs = 30
	defaultSafeSearch = "moderate"
)

// Server implements the MCP web search server using the Brave Search API.
type Server struct {
	mcpserver.BaseServer
	apiKey     string
	maxResults int
	safeSearch string
	client     *http.Client
	handlers   map[string]mcpserver.ToolHandler
}

// Config holds server configuration.
type Config struct {
	APIKey      string
	MaxResults  int
	SafeSearch  string
	TimeoutSecs int
}

// Brave Search API response types.
type braveResponse struct {
	Web *braveWebBlock `json:"web"`
}

type braveWebBlock struct {
	Results []braveResult `json:"results"`
}

type braveResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age"`
}

func NewServer(name, version string, cfg Config) *Server {
	if cfg.MaxResults <= 0 || cfg.MaxResults > 20 {
		cfg.MaxResults = defaultMaxResults
	}
	if cfg.SafeSearch == "" {
		cfg.SafeSearch = defaultSafeSearch
	}
	if cfg.TimeoutSecs <= 0 {
		cfg.TimeoutSecs = defaultTimeoutSecs
	}

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		apiKey:     cfg.APIKey,
		maxResults: cfg.MaxResults,
		safeSearch: cfg.SafeSearch,
		client:     &http.Client{Timeout: time.Duration(cfg.TimeoutSecs) * time.Second},
		handlers:   make(map[string]mcpserver.ToolHandler),
	}
	s.handlers["web_search"] = s.handleWebSearch
	return s
}

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "web_search",
			Description: "Search the web using Brave Search and return ranked results with titles, URLs, and descriptions.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query",
					},
					"count": map[string]interface{}{
						"type":        "integer",
						"description": "Number of results to return (1-20, default 10)",
						"minimum":     1,
						"maximum":     20,
					},
					"safe_search": map[string]interface{}{
						"type":        "string",
						"description": "Safe search level: off, moderate, or strict",
						"enum":        []string{"off", "moderate", "strict"},
					},
				},
				"required": []string{"query"},
			},
		},
	}, nil
}

func (s *Server) Shutdown(_ context.Context) error { return nil }

func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

func (s *Server) handleWebSearch(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query := mcpserver.StrArg(args, "query", "")
	if query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}

	count := s.maxResults
	if c, ok := args["count"].(float64); ok && c >= 1 && c <= 20 {
		count = int(c)
	}

	safeSearch := s.safeSearch
	if ss := mcpserver.StrArg(args, "safe_search", ""); ss != "" {
		safeSearch = ss
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("count", fmt.Sprintf("%d", count))
	params.Set("safesearch", safeSearch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveSearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to build request: %v", err)), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("search request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return mcpserver.ErrorResult("invalid Brave Search API key"), nil
	case http.StatusTooManyRequests:
		return mcpserver.ErrorResult("Brave Search API rate limit exceeded"), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpserver.ErrorResult(fmt.Sprintf("Brave Search API returned HTTP %d", resp.StatusCode)), nil
	}

	var result braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to parse response: %v", err)), nil
	}

	if result.Web == nil || len(result.Web.Results) == 0 {
		return mcpserver.TextResult("No results found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for: %s\n\n", query)
	for i, r := range result.Web.Results {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "   %s\n", r.URL)
		if r.Description != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Description)
		}
		if r.Age != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Age)
		}
		sb.WriteString("\n")
	}

	return mcpserver.TextResult(strings.TrimSpace(sb.String())), nil
}



