package fetch

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
	"golang.org/x/net/html"
)

const (
	defaultMaxBytes      = 1 * 1024 * 1024 // 1MB
	defaultTimeoutSecs   = 30
	defaultUserAgent     = "mcp-fetch/0.1.0"
)

// Server implements the MCP fetch server. It fetches URLs and returns
// LLM-friendly content. HTML is converted to markdown; other content
// types are returned as-is (truncated to maxBytes).
type Server struct {
	mcpserver.BaseServer
	allowedDomains []string
	maxBytes       int64
	timeout        time.Duration
	userAgent      string
	client         *http.Client
	handlers       map[string]mcpserver.ToolHandler
}

// Config holds optional server configuration.
type Config struct {
	AllowedDomains []string
	MaxBytes       int64
	TimeoutSecs    int
	UserAgent      string
}

// NewServer creates a new fetch MCP server.
func NewServer(name, version string, cfg Config) *Server {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultMaxBytes
	}
	if cfg.TimeoutSecs <= 0 {
		cfg.TimeoutSecs = defaultTimeoutSecs
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
	}

	s := &Server{
		BaseServer:     mcpserver.BaseServer{Name: name, Version: version},
		allowedDomains: cfg.AllowedDomains,
		maxBytes:       cfg.MaxBytes,
		timeout:        time.Duration(cfg.TimeoutSecs) * time.Second,
		userAgent:      cfg.UserAgent,
		handlers:       make(map[string]mcpserver.ToolHandler),
	}

	// Clone DefaultTransport so we inherit all production-safe TLS settings
	// (timeouts, ALPN, root CAs, etc.) and only override the dial step for
	// SSRF protection.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = s.safeDialContext
	s.client = &http.Client{
		Timeout:   s.timeout,
		Transport: t,
	}

	s.handlers["fetch_url"] = s.handleFetchURL
	s.handlers["extract_links"] = s.handleExtractLinks
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
			Name:        "fetch_url",
			Description: "Fetch a URL and return its content as plain text. HTML pages are converted to markdown. Respects the configured domain allowlist and size limit.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to fetch (http or https)",
					},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "extract_links",
			Description: "Fetch a URL and return all hyperlinks found on the page as a structured list.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to fetch and extract links from (http or https)",
					},
				},
				"required": []string{"url"},
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

// --- handlers ---

func (s *Server) handleFetchURL(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	rawURL := mcpserver.StrArg(args, "url", "")
	if rawURL == "" {
		return mcpserver.ErrorResult("url is required"), nil
	}
	if err := s.checkURL(rawURL); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	body, contentType, err := s.fetch(ctx, rawURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("fetch failed: %v", err)), nil
	}

	if isHTML(contentType) {
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			return mcpserver.ErrorResult(fmt.Sprintf("failed to parse HTML: %v", err)), nil
		}
		md := htmlToMarkdown(doc)
		return mcpserver.TextResult(md), nil
	}

	return mcpserver.TextResult(body), nil
}

func (s *Server) handleExtractLinks(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	rawURL := mcpserver.StrArg(args, "url", "")
	if rawURL == "" {
		return mcpserver.ErrorResult("url is required"), nil
	}
	if err := s.checkURL(rawURL); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	body, contentType, err := s.fetch(ctx, rawURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("fetch failed: %v", err)), nil
	}
	if !isHTML(contentType) {
		return mcpserver.ErrorResult("page is not HTML; no links to extract"), nil
	}

	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to parse HTML: %v", err)), nil
	}

	base, _ := url.Parse(rawURL)
	links := extractLinks(doc, base)
	if len(links) == 0 {
		return mcpserver.TextResult("No links found."), nil
	}

	var sb strings.Builder
	for _, l := range links {
		if l.Text != "" {
			fmt.Fprintf(&sb, "[%s](%s)\n", l.Text, l.Href)
		} else {
			fmt.Fprintf(&sb, "%s\n", l.Href)
		}
	}
	return mcpserver.TextResult(strings.TrimSpace(sb.String())), nil
}

// --- fetch helper ---

func (s *Server) fetch(ctx context.Context, rawURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	limited := io.LimitReader(resp.Body, s.maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", "", err
	}
	if int64(len(data)) > s.maxBytes {
		data = data[:s.maxBytes]
	}

	contentType := resp.Header.Get("Content-Type")
	return string(data), contentType, nil
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

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

// --- HTML → markdown ---

// skipTags are elements whose content is entirely skipped.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"svg": true, "canvas": true, "form": true,
}

// blockTags get surrounding blank lines.
var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"main": true, "figure": true, "figcaption": true, "aside": true,
	"header": true, "footer": true, "nav": true,
}

type link struct {
	Href string
	Text string
}

func htmlToMarkdown(doc *html.Node) string {
	var sb strings.Builder
	walkMD(&sb, doc)
	// Collapse runs of 3+ blank lines into 2.
	result := sb.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}

func walkMD(sb *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		text := strings.ReplaceAll(n.Data, "\n", " ")
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			sb.WriteString(text)
			sb.WriteString(" ")
		}
		return
	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		if skipTags[tag] {
			return
		}

		switch tag {
		case "head":
			return

		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(tag[1]-'0')
			sb.WriteString("\n\n")
			sb.WriteString(strings.Repeat("#", level))
			sb.WriteString(" ")
			walkChildren(sb, n)
			sb.WriteString("\n\n")
			return

		case "p":
			sb.WriteString("\n\n")
			walkChildren(sb, n)
			sb.WriteString("\n\n")
			return

		case "br":
			sb.WriteString("\n")
			return

		case "hr":
			sb.WriteString("\n\n---\n\n")
			return

		case "strong", "b":
			sb.WriteString("**")
			walkChildren(sb, n)
			sb.WriteString("**")
			return

		case "em", "i":
			sb.WriteString("*")
			walkChildren(sb, n)
			sb.WriteString("*")
			return

		case "code":
			if n.Parent != nil && n.Parent.Data == "pre" {
				walkChildren(sb, n)
				return
			}
			sb.WriteString("`")
			walkChildren(sb, n)
			sb.WriteString("`")
			return

		case "pre":
			sb.WriteString("\n\n```\n")
			walkChildren(sb, n)
			sb.WriteString("\n```\n\n")
			return

		case "blockquote":
			var inner strings.Builder
			walkChildren(&inner, n)
			lines := strings.Split(strings.TrimSpace(inner.String()), "\n")
			sb.WriteString("\n\n")
			for _, line := range lines {
				sb.WriteString("> ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
			return

		case "ul":
			sb.WriteString("\n\n")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "li" {
					sb.WriteString("- ")
					var inner strings.Builder
					walkChildren(&inner, c)
					sb.WriteString(strings.TrimSpace(inner.String()))
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
			return

		case "ol":
			sb.WriteString("\n\n")
			i := 1
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && c.Data == "li" {
					fmt.Fprintf(sb, "%d. ", i)
					i++
					var inner strings.Builder
					walkChildren(&inner, c)
					sb.WriteString(strings.TrimSpace(inner.String()))
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
			return

		case "a":
			href := attrVal(n, "href")
			var inner strings.Builder
			walkChildren(&inner, n)
			text := strings.TrimSpace(inner.String())
			if href != "" && text != "" {
				fmt.Fprintf(sb, "[%s](%s)", text, href)
			} else if text != "" {
				sb.WriteString(text)
			}
			return

		case "img":
			alt := attrVal(n, "alt")
			if alt != "" {
				fmt.Fprintf(sb, "[image: %s]", alt)
			}
			return

		case "li":
			// handled by ul/ol
			walkChildren(sb, n)
			return

		case "table":
			// Basic table: collect rows of cells
			sb.WriteString("\n\n")
			writeTable(sb, n)
			sb.WriteString("\n")
			return
		}

		if blockTags[tag] {
			sb.WriteString("\n\n")
			walkChildren(sb, n)
			sb.WriteString("\n\n")
			return
		}
	}

	walkChildren(sb, n)
}

func walkChildren(sb *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkMD(sb, c)
	}
}

func writeTable(sb *strings.Builder, table *html.Node) {
	var rows [][]string
	collectTableRows(table, &rows)
	if len(rows) == 0 {
		return
	}
	// Find max columns
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	if cols == 0 {
		return
	}
	// Write header row + separator
	writeTableRow(sb, rows[0], cols)
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	writeTableRow(sb, sep, cols)
	for _, row := range rows[1:] {
		writeTableRow(sb, row, cols)
	}
}

func writeTableRow(sb *strings.Builder, cells []string, cols int) {
	sb.WriteString("|")
	for i := 0; i < cols; i++ {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		sb.WriteString(" ")
		sb.WriteString(cell)
		sb.WriteString(" |")
	}
	sb.WriteString("\n")
}

func collectTableRows(n *html.Node, rows *[][]string) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "tr":
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
					var inner strings.Builder
					walkChildren(&inner, c)
					cells = append(cells, strings.TrimSpace(inner.String()))
				}
			}
			if len(cells) > 0 {
				*rows = append(*rows, cells)
			}
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectTableRows(c, rows)
	}
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// --- link extraction ---

func extractLinks(doc *html.Node, base *url.URL) []link {
	var links []link
	walkLinks(doc, base, &links)
	// Deduplicate by href
	seen := make(map[string]bool)
	out := links[:0]
	for _, l := range links {
		if !seen[l.Href] {
			seen[l.Href] = true
			out = append(out, l)
		}
	}
	return out
}

func walkLinks(n *html.Node, base *url.URL, links *[]link) {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := attrVal(n, "href")
		if href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
			// Resolve relative URLs
			if base != nil {
				ref, err := url.Parse(href)
				if err == nil {
					href = base.ResolveReference(ref).String()
				}
			}
			var inner strings.Builder
			walkChildren(&inner, n)
			text := strings.TrimSpace(inner.String())
			*links = append(*links, link{Href: href, Text: text})
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkLinks(c, base, links)
	}
}

// --- shared helpers ---



