package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

const (
	defaultMaxResponseBytes = 5 * 1024 * 1024 // 5MB
	defaultTimeout          = 30 * time.Second
)

// GQLResponse is the top-level structure of a GraphQL HTTP response.
type GQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GQLError      `json:"errors,omitempty"`
}

// GQLError represents a single GraphQL error returned from the server.
type GQLError struct {
	Message   string                 `json:"message"`
	Locations []GQLErrorLocation     `json:"locations,omitempty"`
	Path      []interface{}          `json:"path,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// GQLErrorLocation identifies a source location for a GraphQL error.
type GQLErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Config holds the configuration for a GraphQL MCP server.
type Config struct {
	// Endpoint is the full GraphQL endpoint URL, e.g. https://api.example.com/graphql
	Endpoint string
	// Headers are default HTTP headers sent with every request (e.g. Authorization).
	Headers map[string]string
	// Timeout is the per-request HTTP timeout. Defaults to 30s.
	Timeout time.Duration
	// AllowMutations controls whether the graphql_mutate tool is exposed.
	AllowMutations bool
	// AllowIntrospect controls whether the graphql_introspect and
	// graphql_describe_type tools are exposed.
	AllowIntrospect bool
	// MaxResponseBytes is the maximum number of bytes read from the response body.
	// Defaults to 5MB.
	MaxResponseBytes int64
}

// Server implements an MCP server that executes arbitrary GraphQL queries and
// mutations against a configured endpoint. It intentionally does not implement
// its own HTTP transport — all requests go through DoRequest.
type Server struct {
	mcpserver.BaseServer
	cfg      Config
	client   *http.Client
	handlers map[string]mcpserver.ToolHandler
}

// NewServer creates a new GraphQL MCP server from the supplied Config.
func NewServer(cfg Config) *Server {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = defaultMaxResponseBytes
	}
	if cfg.Headers == nil {
		cfg.Headers = make(map[string]string)
	}

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: "mcp-graphql", Version: "0.1.0"},
		cfg:        cfg,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: buildTransport(),
		},
		handlers: make(map[string]mcpserver.ToolHandler),
	}

	s.handlers["graphql_query"] = s.handleQuery
	if cfg.AllowMutations {
		s.handlers["graphql_mutate"] = s.handleMutate
	}
	if cfg.AllowIntrospect {
		s.handlers["graphql_introspect"] = s.handleIntrospect
		s.handlers["graphql_describe_type"] = s.handleDescribeType
	}

	return s
}

// ListTools returns all available tools based on the server configuration.
func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	tools := []mcpserver.Tool{
		{
			Name:        "graphql_query",
			Description: "Execute a GraphQL query against the configured endpoint. Returns the response data as JSON.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The GraphQL query string",
					},
					"variables": map[string]interface{}{
						"type":        "string",
						"description": "Optional JSON object of query variables",
					},
					"operation_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional operation name when the query document contains multiple operations",
					},
				},
				"required": []string{"query"},
			},
		},
	}

	if s.cfg.AllowMutations {
		tools = append(tools, mcpserver.Tool{
			Name:        "graphql_mutate",
			Description: "Execute a GraphQL mutation against the configured endpoint. Returns the response data as JSON.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The GraphQL mutation string",
					},
					"variables": map[string]interface{}{
						"type":        "string",
						"description": "Optional JSON object of mutation variables",
					},
					"operation_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional operation name",
					},
				},
				"required": []string{"query"},
			},
		})
	}

	if s.cfg.AllowIntrospect {
		tools = append(tools,
			mcpserver.Tool{
				Name:        "graphql_introspect",
				Description: "Introspect the GraphQL schema. Returns the full schema including all types, fields, and descriptions. WARNING: the result can be very large (hundreds of KB). Prefer graphql_describe_type when you only need a specific type.",
				InputSchema: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			mcpserver.Tool{
				Name:        "graphql_describe_type",
				Description: "Describe a specific GraphQL type, including its fields, arguments, and descriptions.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type_name": map[string]interface{}{
							"type":        "string",
							"description": "The name of the GraphQL type to describe",
						},
					},
					"required": []string{"type_name"},
				},
			},
		)
	}

	return tools, nil
}

// CallTool dispatches a tool call to the appropriate handler.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

// Shutdown cleans up HTTP client resources.
func (s *Server) Shutdown(_ context.Context) error {
	s.client.CloseIdleConnections()
	return nil
}

// --- Tool handlers ---

func (s *Server) handleQuery(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}
	vars, err := ParseVariables(args, "variables")
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("invalid variables: %v", err)), nil
	}
	opName, _ := args["operation_name"].(string)
	return Execute(ctx, s, query, vars, opName)
}

func (s *Server) handleMutate(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}
	vars, err := ParseVariables(args, "variables")
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("invalid variables: %v", err)), nil
	}
	opName, _ := args["operation_name"].(string)
	return Execute(ctx, s, query, vars, opName)
}

func (s *Server) handleIntrospect(ctx context.Context, _ map[string]interface{}) (*mcpserver.CallToolResult, error) {
	const introspectionQuery = `{
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types {
      name
      kind
      description
      fields(includeDeprecated: true) {
        name
        description
        type { name kind ofType { name kind } }
        isDeprecated
        deprecationReason
      }
    }
  }
}`
	resp, err := DoRequest(ctx, s, introspectionQuery, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}
	return mcpserver.TextResult(prettyJSON(resp.Data)), nil
}

func (s *Server) handleDescribeType(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	typeName, ok := args["type_name"].(string)
	if !ok || typeName == "" {
		return mcpserver.ErrorResult("type_name is required"), nil
	}
	query := fmt.Sprintf(`{
  __type(name: %q) {
    name
    kind
    description
    fields(includeDeprecated: true) {
      name
      description
      args {
        name
        description
        type { name kind ofType { name kind } }
        defaultValue
      }
      type { name kind ofType { name kind } }
      isDeprecated
      deprecationReason
    }
    inputFields {
      name
      description
      type { name kind ofType { name kind } }
      defaultValue
    }
    enumValues(includeDeprecated: true) {
      name
      description
      isDeprecated
      deprecationReason
    }
    possibleTypes { name kind }
    interfaces { name kind }
  }
}`, typeName)
	resp, err := DoRequest(ctx, s, query, nil, "")
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}
	return mcpserver.TextResult(prettyJSON(resp.Data)), nil
}

// --- Exported helpers used by composed servers (e.g. mcp-netbox) ---

// DoRequest executes a raw GraphQL request and returns the parsed GQLResponse.
// It does not interpret errors — callers should check resp.Errors themselves.
func DoRequest(ctx context.Context, s *Server, query string, variables map[string]interface{}, operationName string) (*GQLResponse, error) {
	payload := map[string]interface{}{
		"query": query,
	}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	if operationName != "" {
		payload["operationName"] = operationName
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}

	httpResp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	limited := io.LimitReader(httpResp.Body, s.cfg.MaxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if int64(len(data)) > s.cfg.MaxResponseBytes {
		return nil, fmt.Errorf("response exceeds maximum size of %d bytes", s.cfg.MaxResponseBytes)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		preview := string(data)
		if len(preview) > 512 {
			preview = preview[:512] + "..."
		}
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, preview)
	}

	var gqlResp GQLResponse
	if err := json.Unmarshal(data, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &gqlResp, nil
}

// Execute executes a GraphQL query/mutation and returns a CallToolResult with
// the response data as formatted JSON. It is the primary entry point for
// composed servers that need to run a query and return the raw result.
func Execute(ctx context.Context, s *Server, query string, variables map[string]interface{}, operationName string) (*mcpserver.CallToolResult, error) {
	resp, err := DoRequest(ctx, s, query, variables, operationName)
	if err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}
	if isNullData(resp.Data) && len(resp.Errors) > 0 {
		return mcpserver.ErrorResult(formatErrors(resp.Errors)), nil
	}
	var sb strings.Builder
	if len(resp.Errors) > 0 {
		sb.WriteString("Partial errors:\n")
		sb.WriteString(formatErrors(resp.Errors))
		sb.WriteString("\n\nData:\n")
	}
	sb.WriteString(prettyJSON(resp.Data))
	return mcpserver.TextResult(sb.String()), nil
}

// ParseVariables parses the named argument from args as a JSON object string
// and returns it as a map. Returns nil if the argument is absent or empty.
func ParseVariables(args map[string]interface{}, key string) (map[string]interface{}, error) {
	raw, ok := args[key]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, nil
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return nil, nil
	}
	var vars map[string]interface{}
	if err := json.Unmarshal([]byte(s), &vars); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	return vars, nil
}

// --- internal helpers ---

func isNullData(data json.RawMessage) bool {
	if len(data) == 0 {
		return true
	}
	s := strings.TrimSpace(string(data))
	return s == "null" || s == ""
}

func formatErrors(errs []GQLError) string {
	var sb strings.Builder
	for i, e := range errs {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(e.Message)
	}
	return sb.String()
}

func prettyJSON(data json.RawMessage) string {
	if len(data) == 0 {
		return "null"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data)
	}
	return buf.String()
}

// --- network safety ---

// buildTransport returns an http.Transport appropriate for the current
// environment.  When HTTP_PROXY or HTTPS_PROXY is set the container is behind
// an egress proxy (squid) that enforces its own allowlist; in that case we use
// http.ProxyFromEnvironment and the standard dialer so the proxy can be reached
// (the proxy's IP is on a Docker bridge — safeDialContext would block it).
// Without a proxy we install safeDialContext for in-process SSRF protection.
func buildTransport() *http.Transport {
	if proxyURL := proxyFromEnv(); proxyURL != nil {
		return &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
	}
	return &http.Transport{
		DialContext: safeDialContext,
	}
}

// proxyFromEnv returns the proxy URL from HTTPS_PROXY / HTTP_PROXY, or nil.
func proxyFromEnv() *url.URL {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if val := os.Getenv(key); val != "" {
			u, err := url.Parse(val)
			if err == nil {
				return u
			}
		}
	}
	return nil
}

// safeDialContext resolves the hostname and rejects private/loopback IPs.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && isPrivateIP(ip) {
			return nil, fmt.Errorf("access to private IP %s is not allowed", ip)
		}
	}
	d := &net.Dialer{}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
}

func isPrivateIP(ip net.IP) bool {
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range privateBlocks {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// --- result helpers ---


