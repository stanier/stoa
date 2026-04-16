package acpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/mcpserver"
)

const (
	defaultTimeoutSecs = 120
)

// Config holds optional server configuration.
type Config struct {
	DefaultEndpoint string
	APIKey          string
	TimeoutSecs     int
}

// Server implements the MCP ACP client server.
type Server struct {
	mcpserver.BaseServer
	cfg      Config
	client   *http.Client
	handlers map[string]mcpserver.ToolHandler
}

// NewServer creates a new ACP client MCP server.
func NewServer(name, version string, cfg Config) *Server {
	if cfg.TimeoutSecs <= 0 {
		cfg.TimeoutSecs = defaultTimeoutSecs
	}

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		cfg:        cfg,
		handlers: make(map[string]mcpserver.ToolHandler),
	}

	// Clone DefaultTransport and override dial for SSRF protection.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = s.safeDialContext
	s.client = &http.Client{
		Timeout:   time.Duration(cfg.TimeoutSecs) * time.Second,
		Transport: t,
	}

	s.handlers["acp_discover"] = s.handleDiscover
	s.handlers["acp_run"] = s.handleRun
	s.handlers["acp_run_stream"] = s.handleRunStream
	return s
}

// safeDialContext blocks requests to localhost and private IPs.
func (s *Server) safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(host) {
	case "localhost", "ip6-localhost", "ip6-loopback":
		return nil, fmt.Errorf("access to %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return nil, fmt.Errorf("access to private IP %s is not allowed", ip)
		}
		d := &net.Dialer{}
		return d.DialContext(ctx, network, net.JoinHostPort(host, port))
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

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "acp_discover",
			Description: "List agents available at an ACP endpoint. Returns agent names, descriptions, and visibility.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"endpoint": map[string]interface{}{
						"type":        "string",
						"description": "Base URL of the ACP server, e.g. \"https://agents.example.com\"",
					},
				},
				"required": []string{"endpoint"},
			},
		},
		{
			Name:        "acp_run",
			Description: "Call an ACP agent synchronously and return its output.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"endpoint": map[string]interface{}{
						"type":        "string",
						"description": "Base URL of the ACP server",
					},
					"agent": map[string]interface{}{
						"type":        "string",
						"description": "Name of the agent to invoke",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "User message text to send to the agent",
					},
					"session_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional session ID for multi-turn conversations",
					},
				},
				"required": []string{"endpoint", "agent", "message"},
			},
		},
		{
			Name:        "acp_run_stream",
			Description: "Call an ACP agent with SSE streaming and return the assembled output once complete.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"endpoint": map[string]interface{}{
						"type":        "string",
						"description": "Base URL of the ACP server",
					},
					"agent": map[string]interface{}{
						"type":        "string",
						"description": "Name of the agent to invoke",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "User message text to send to the agent",
					},
					"session_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional session ID for multi-turn conversations",
					},
				},
				"required": []string{"endpoint", "agent", "message"},
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

func (s *Server) handleDiscover(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	endpoint := mcpserver.StrArg(args, "endpoint", s.cfg.DefaultEndpoint)
	if endpoint == "" {
		return mcpserver.ErrorResult("endpoint is required"), nil
	}
	if err := s.checkURL(endpoint); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	reqURL := strings.TrimRight(endpoint, "/") + "/acp/agents"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to build request: %v", err)), nil
	}
	s.setAuth(req)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpserver.ErrorResult(fmt.Sprintf("ACP server returned HTTP %d", resp.StatusCode)), nil
	}

	var list agentproto.ACPAgentsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to decode response: %v", err)), nil
	}

	if len(list.Agents) == 0 {
		return mcpserver.TextResult("No agents found at this endpoint."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d agent(s) at %s:\n\n", len(list.Agents), endpoint)
	for _, a := range list.Agents {
		name := a.Name
		if a.DisplayName != "" {
			name = fmt.Sprintf("%s (%s)", a.DisplayName, a.Name)
		}
		fmt.Fprintf(&sb, "- **%s**", name)
		if a.Visibility != "" {
			fmt.Fprintf(&sb, " [%s]", a.Visibility)
		}
		if a.Description != "" {
			fmt.Fprintf(&sb, ": %s", a.Description)
		}
		sb.WriteString("\n")
	}
	return mcpserver.TextResult(strings.TrimSpace(sb.String())), nil
}

func (s *Server) handleRun(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	endpoint := mcpserver.StrArg(args, "endpoint", s.cfg.DefaultEndpoint)
	agent := mcpserver.StrArg(args, "agent", "")
	message := mcpserver.StrArg(args, "message", "")
	sessionID := mcpserver.StrArg(args, "session_id", "")

	if endpoint == "" {
		return mcpserver.ErrorResult("endpoint is required"), nil
	}
	if agent == "" {
		return mcpserver.ErrorResult("agent is required"), nil
	}
	if message == "" {
		return mcpserver.ErrorResult("message is required"), nil
	}
	if err := s.checkURL(endpoint); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	runReq := agentproto.ACPRunRequest{
		AgentName: agent,
		Mode:      "sync",
		Messages: []agentproto.ACPMessage{
			{
				Role: "user",
				Parts: []agentproto.ACPPart{
					{ContentType: "text/plain", Content: message},
				},
			},
		},
	}
	if sessionID != "" {
		runReq.SessionID = sessionID
	}

	body, err := json.Marshal(runReq)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to encode request: %v", err)), nil
	}

	reqURL := strings.TrimRight(endpoint, "/") + "/acp/runs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to build request: %v", err)), nil
	}
	s.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpserver.ErrorResult(fmt.Sprintf("ACP server returned HTTP %d", resp.StatusCode)), nil
	}

	var runResp agentproto.ACPRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to decode response: %v", err)), nil
	}

	if runResp.Status == "failed" {
		msg := runResp.Error
		if msg == "" {
			msg = "run failed (no error detail)"
		}
		return mcpserver.ErrorResult(fmt.Sprintf("agent run failed: %s", msg)), nil
	}

	content := extractContent(runResp.Output)
	var sb strings.Builder
	fmt.Fprintf(&sb, "run_id: %s\n", runResp.ID)
	if runResp.SessionID != "" {
		fmt.Fprintf(&sb, "session_id: %s\n", runResp.SessionID)
	}
	fmt.Fprintf(&sb, "status: %s\n\n", runResp.Status)
	sb.WriteString(content)
	return mcpserver.TextResult(strings.TrimSpace(sb.String())), nil
}

func (s *Server) handleRunStream(ctx context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	endpoint := mcpserver.StrArg(args, "endpoint", s.cfg.DefaultEndpoint)
	agent := mcpserver.StrArg(args, "agent", "")
	message := mcpserver.StrArg(args, "message", "")
	sessionID := mcpserver.StrArg(args, "session_id", "")

	if endpoint == "" {
		return mcpserver.ErrorResult("endpoint is required"), nil
	}
	if agent == "" {
		return mcpserver.ErrorResult("agent is required"), nil
	}
	if message == "" {
		return mcpserver.ErrorResult("message is required"), nil
	}
	if err := s.checkURL(endpoint); err != nil {
		return mcpserver.ErrorResult(err.Error()), nil
	}

	runReq := agentproto.ACPRunRequest{
		AgentName: agent,
		Mode:      "stream",
		Messages: []agentproto.ACPMessage{
			{
				Role: "user",
				Parts: []agentproto.ACPPart{
					{ContentType: "text/plain", Content: message},
				},
			},
		},
	}
	if sessionID != "" {
		runReq.SessionID = sessionID
	}

	body, err := json.Marshal(runReq)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to encode request: %v", err)), nil
	}

	reqURL := strings.TrimRight(endpoint, "/") + "/acp/runs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("failed to build request: %v", err)), nil
	}
	s.setAuth(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpserver.ErrorResult(fmt.Sprintf("ACP server returned HTTP %d", resp.StatusCode)), nil
	}

	// Parse SSE stream
	var (
		runID     string
		outSessID string
		status    string
		parts     []string
		runErr    string
	)

	scanner := bufio.NewScanner(resp.Body)
	var eventType string
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Dispatch event
			if dataBuf.Len() > 0 {
				data := dataBuf.String()
				dataBuf.Reset()

				switch eventType {
				case "run.created", "run.in_progress":
					var r agentproto.ACPRunResponse
					if json.Unmarshal([]byte(data), &r) == nil {
						if runID == "" {
							runID = r.ID
						}
						if outSessID == "" {
							outSessID = r.SessionID
						}
					}
				case "message.part":
					var part agentproto.ACPPart
					if json.Unmarshal([]byte(data), &part) == nil && part.Content != "" {
						parts = append(parts, part.Content)
					}
				case "run.completed":
					var r agentproto.ACPRunResponse
					if json.Unmarshal([]byte(data), &r) == nil {
						status = r.Status
						if runID == "" {
							runID = r.ID
						}
						if outSessID == "" {
							outSessID = r.SessionID
						}
						// Use output field if parts are empty
						if len(parts) == 0 {
							c := extractContent(r.Output)
							if c != "" {
								parts = append(parts, c)
							}
						}
					}
					goto done
				case "run.failed":
					var r agentproto.ACPRunResponse
					if json.Unmarshal([]byte(data), &r) == nil {
						runErr = r.Error
						status = "failed"
						if runID == "" {
							runID = r.ID
						}
					}
					goto done
				}
			}
			eventType = ""
			continue
		}

		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		// ignore id: and comment lines
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return mcpserver.ErrorResult(fmt.Sprintf("SSE stream error: %v", err)), nil
	}

done:
	if status == "failed" {
		if runErr == "" {
			runErr = "run failed (no error detail)"
		}
		return mcpserver.ErrorResult(fmt.Sprintf("agent run failed: %s", runErr)), nil
	}

	content := strings.Join(parts, "")
	var sb strings.Builder
	if runID != "" {
		fmt.Fprintf(&sb, "run_id: %s\n", runID)
	}
	if outSessID != "" {
		fmt.Fprintf(&sb, "session_id: %s\n", outSessID)
	}
	if status != "" {
		fmt.Fprintf(&sb, "status: %s\n\n", status)
	}
	sb.WriteString(content)
	return mcpserver.TextResult(strings.TrimSpace(sb.String())), nil
}

// --- helpers ---

func (s *Server) setAuth(req *http.Request) {
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}
}

func (s *Server) checkURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https URLs are allowed")
	}
	return nil
}

// extractContent collects text/plain content from ACP output messages.
func extractContent(output []agentproto.ACPMessage) string {
	var sb strings.Builder
	for _, msg := range output {
		for _, part := range msg.Parts {
			if strings.HasPrefix(part.ContentType, "text/") && part.Content != "" {
				sb.WriteString(part.Content)
			}
		}
	}
	return sb.String()
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
