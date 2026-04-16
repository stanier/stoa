package httpserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

const (
	defaultMaxBytes    = 1 * 1024 * 1024 // 1MB
	defaultTimeoutSecs = 30
)

// Server implements the MCP HTTP server. It makes arbitrary HTTP requests
// to external APIs, supporting GET/POST/PUT/DELETE/PATCH with custom headers
// and request bodies. Distinct from mcp-fetch: this is for calling APIs,
// not fetching web pages (no HTML conversion, returns status codes, supports
// request bodies and arbitrary methods).
type Server struct {
	mcpserver.BaseServer
	allowedDomains []string
	maxBytes       int64
	timeout        time.Duration
	defaultHeaders map[string]string
	client         *http.Client
	handlers       map[string]mcpserver.ToolHandler
}

// Config holds optional server configuration.
type Config struct {
	AllowedDomains []string
	MaxBytes       int64
	TimeoutSecs    int
	// DefaultHeaders are merged into every request before per-call headers.
	// Useful for embedding auth tokens when running as a configured service.
	DefaultHeaders map[string]string
}

// NewServer creates a new HTTP MCP server.
func NewServer(name, version string, cfg Config) *Server {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	if cfg.TimeoutSecs <= 0 {
		cfg.TimeoutSecs = defaultTimeoutSecs
	}
	if cfg.DefaultHeaders == nil {
		cfg.DefaultHeaders = make(map[string]string)
	}

	s := &Server{
		BaseServer:     mcpserver.BaseServer{Name: name, Version: version},
		allowedDomains: cfg.AllowedDomains,
		maxBytes:       cfg.MaxBytes,
		timeout:        time.Duration(cfg.TimeoutSecs) * time.Second,
		defaultHeaders: cfg.DefaultHeaders,
		handlers:       make(map[string]mcpserver.ToolHandler),
	}

	s.client = &http.Client{
		Timeout: s.timeout,
		Transport: &http.Transport{
			DialContext: s.safeDialContext,
		},
	}

	s.handlers["http_request"] = s.handleRequest
	return s
}

// safeDialContext blocks requests to localhost and private IPs before connecting.
// It checks the hostname before DNS resolution (to catch "localhost", "ip6-localhost",
// etc.) and also checks all resolved IPs (to catch private IP literals and DNS
// rebinding where the hostname resolves to a private address).
func (s *Server) safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// Pre-DNS hostname check: block well-known loopback hostnames.
	switch strings.ToLower(host) {
	case "localhost", "ip6-localhost", "ip6-loopback":
		return nil, fmt.Errorf("access to %q is not allowed", host)
	}
	// If the host is already an IP literal, check it directly without DNS.
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return nil, fmt.Errorf("access to private IP %s is not allowed", ip)
		}
		d := &net.Dialer{}
		return d.DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	// Resolve and check every returned address.
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

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "http_request",
			Description: "Make an HTTP request to an external API. Returns the response status code, relevant headers, and body. Supports GET, POST, PUT, DELETE, and PATCH. Custom request headers (e.g. Authorization) and a request body (for POST/PUT/PATCH) can be provided. Respects the configured domain allowlist and response size limit.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"method": map[string]interface{}{
						"type":        "string",
						"description": "HTTP method: GET, POST, PUT, DELETE, or PATCH",
						"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Request URL (http or https)",
					},
					"headers": map[string]interface{}{
						"type":        "object",
						"description": `Per-request headers, e.g. {"Authorization": "Bearer token", "Content-Type": "application/json"}`,
						"additionalProperties": map[string]interface{}{
							"type": "string",
						},
					},
					"body": map[string]interface{}{
						"type":        "string",
						"description": "Request body string for POST/PUT/PATCH (e.g. a JSON payload). Omit for GET/DELETE.",
					},
				},
				"required": []string{"method", "url"},
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

// --- handler ---

func (s *Server) handleRequest(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	method := strings.ToUpper(mcpserver.StrArg(args, "method", "GET"))
	rawURL := mcpserver.StrArg(args, "url", "")
	bodyStr := mcpserver.StrArg(args, "body", "")

	if rawURL == "" {
		return mcpserver.ErrorResult("url is required"), nil
	}
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
	default:
		return mcpserver.ErrorResult(fmt.Sprintf("unsupported method %q; use GET, POST, PUT, DELETE, or PATCH", method)), nil
	}
	if err := s.checkURL(rawURL); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	// Collect per-request headers from args.
	reqHeaders := make(map[string]string)
	if h, ok := args["headers"]; ok {
		if hmap, ok := h.(map[string]interface{}); ok {
			for k, v := range hmap {
				if vs, ok := v.(string); ok {
					reqHeaders[k] = vs
				}
			}
		}
	}

	var bodyReader io.Reader
	if bodyStr != "" {
		bodyReader = strings.NewReader(bodyStr)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to build request: %v", err)), nil
	}

	// Default headers first; per-request headers override.
	for k, v := range s.defaultHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range reqHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, s.maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to read response body: %v", err)), nil
	}

	truncated := int64(len(data)) > s.maxBytes
	if truncated {
		data = data[:s.maxBytes]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Status: %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	for _, h := range []string{"Content-Type", "Content-Length", "X-Request-Id", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if v := resp.Header.Get(h); v != "" {
			fmt.Fprintf(&sb, "%s: %s\n", h, v)
		}
	}
	sb.WriteString("\n")
	sb.Write(data)
	if truncated {
		fmt.Fprintf(&sb, "\n\n[Response truncated at %d bytes]", s.maxBytes)
	}

	return mcpserver.TextResult(sb.String()), nil
}

// --- URL validation ---

func (s *Server) checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are allowed")
	}
	if len(s.allowedDomains) > 0 {
		host := strings.ToLower(u.Hostname())
		allowed := false
		for _, d := range s.allowedDomains {
			d = strings.ToLower(d)
			if host == d || strings.HasSuffix(host, "."+d) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("domain %q is not in the allowed list", u.Hostname())
		}
	}
	return nil
}

// isPrivateIP reports whether ip is in a private or loopback range.
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

// --- shared helpers ---



