package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Config holds Prometheus connection configuration.
type Config struct {
	BaseURL  string
	Token    string        // bearer token (takes priority over basic auth)
	Username string        // basic auth
	Password string        // basic auth
	OrgID    string        // X-Scope-OrgID for Mimir/Cortex
	Timeout  time.Duration
}

// Server implements an MCP server for Prometheus/PromQL.
type Server struct {
	mcpserver.BaseServer
	cfg      Config
	client   *http.Client
	handlers map[string]mcpserver.ToolHandler
}

// Prometheus API response envelope types.
type promResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
	Warnings  []string        `json:"warnings"`
}

type promData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type vectorSample struct {
	Metric map[string]string  `json:"metric"`
	Value  [2]json.RawMessage `json:"value"` // [timestamp, value_string]
}

type matrixSample struct {
	Metric map[string]string    `json:"metric"`
	Values [][2]json.RawMessage `json:"values"`
}

// NewServer creates a new Prometheus MCP server.
func NewServer(name, version string, cfg Config) *Server {
	// Strip trailing slash from BaseURL
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		cfg:        cfg,
		client:   &http.Client{Timeout: cfg.Timeout},
		handlers: make(map[string]mcpserver.ToolHandler),
	}

	s.handlers["promql_query"] = s.handleQuery
	s.handlers["promql_query_range"] = s.handleQueryRange
	s.handlers["promql_list_metrics"] = s.handleListMetrics
	s.handlers["promql_get_labels"] = s.handleGetLabels
	s.handlers["promql_get_label_values"] = s.handleGetLabelValues
	s.handlers["promql_find_series"] = s.handleFindSeries
	s.handlers["promql_get_targets"] = s.handleGetTargets
	s.handlers["promql_get_alerts"] = s.handleGetAlerts
	s.handlers["promql_get_rules"] = s.handleGetRules
	s.handlers["promql_metric_metadata"] = s.handleMetricMetadata

	return s
}

// addAuth adds authentication headers to an HTTP request.
func (s *Server) addAuth(req *http.Request) {
	if s.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	} else if s.cfg.Username != "" {
		req.SetBasicAuth(s.cfg.Username, s.cfg.Password)
	}
	if s.cfg.OrgID != "" {
		req.Header.Set("X-Scope-OrgID", s.cfg.OrgID)
	}
}

// doGet makes a GET request, handles auth, and returns the raw data field on success.
func (s *Server) doGet(ctx context.Context, apiURL string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	s.addAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("parse Prometheus response: %w", err)
	}

	if pr.Status != "success" {
		if pr.Error != "" {
			return nil, fmt.Errorf("Prometheus error (%s): %s", pr.ErrorType, pr.Error)
		}
		return nil, fmt.Errorf("Prometheus returned non-success status: %s", pr.Status)
	}

	return pr.Data, nil
}

// ListTools returns all available Prometheus tools.
func (s *Server) ListTools(ctx context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "promql_query",
			Description: "Execute an instant PromQL query against Prometheus. Returns current or point-in-time values for metrics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "PromQL expression to evaluate",
					},
					"time": map[string]interface{}{
						"type":        "string",
						"description": "Evaluation timestamp in RFC3339 or Unix format (optional, defaults to now)",
					},
					"timeout": map[string]interface{}{
						"type":        "string",
						"description": "Query timeout, e.g. '30s' (optional)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "promql_query_range",
			Description: "Execute a range PromQL query returning a time series over a given time window.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "PromQL expression to evaluate",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time in RFC3339 or Unix format",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time in RFC3339 or Unix format",
					},
					"step": map[string]interface{}{
						"type":        "string",
						"description": "Step resolution, e.g. '15s', '1m', '5m'",
					},
					"timeout": map[string]interface{}{
						"type":        "string",
						"description": "Query timeout, e.g. '30s' (optional)",
					},
				},
				"required": []string{"query", "start", "end", "step"},
			},
		},
		{
			Name:        "promql_list_metrics",
			Description: "List available metric names in Prometheus, with optional substring filtering. Prefer this over promql_get_label_values(label=\"__name__\") — it applies client-side substring filtering and formats output more readably.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"match": map[string]interface{}{
						"type":        "string",
						"description": "Case-insensitive substring to filter metric names (optional)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results to return (default: 100)",
					},
				},
			},
		},
		{
			Name:        "promql_get_labels",
			Description: "Get all label names available in Prometheus, optionally filtered by series selector and time range.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"match": map[string]interface{}{
						"type":        "string",
						"description": "Series selector to restrict label names, e.g. '{job=\"prometheus\"}' (optional)",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time in RFC3339 or Unix format (optional)",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time in RFC3339 or Unix format (optional)",
					},
				},
			},
		},
		{
			Name:        "promql_get_label_values",
			Description: "Get all values for a specific label name.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label": map[string]interface{}{
						"type":        "string",
						"description": "Label name to look up values for",
					},
					"match": map[string]interface{}{
						"type":        "string",
						"description": "Series selector to restrict values (optional)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of values to return (default: 100)",
					},
				},
				"required": []string{"label"},
			},
		},
		{
			Name:        "promql_find_series",
			Description: "Find all time series matching a label selector.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"match": map[string]interface{}{
						"type":        "string",
						"description": "Series selector, e.g. '{__name__=~\"go_.*\"}' or 'up{job=\"prometheus\"}'",
					},
					"start": map[string]interface{}{
						"type":        "string",
						"description": "Start time in RFC3339 or Unix format (optional)",
					},
					"end": map[string]interface{}{
						"type":        "string",
						"description": "End time in RFC3339 or Unix format (optional)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of series to return (default: 50)",
					},
				},
				"required": []string{"match"},
			},
		},
		{
			Name:        "promql_get_targets",
			Description: "Get Prometheus scrape targets and their health status.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"state": map[string]interface{}{
						"type":        "string",
						"description": "Filter by target state: 'active' or 'dropped' (default: 'active')",
					},
					"job": map[string]interface{}{
						"type":        "string",
						"description": "Filter by job name (optional, client-side filter)",
					},
				},
			},
		},
		{
			Name:        "promql_get_alerts",
			Description: "Get currently firing or pending alerts from Prometheus.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "promql_get_rules",
			Description: "Get Prometheus recording and alerting rules.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type": map[string]interface{}{
						"type":        "string",
						"description": "Filter by rule type: 'alert' or 'record' (optional)",
					},
					"rule_name": map[string]interface{}{
						"type":        "string",
						"description": "Filter by rule name (case-insensitive partial match, optional)",
					},
				},
			},
		},
		{
			Name:        "promql_metric_metadata",
			Description: "Get metadata (type, help text, unit) for metrics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"metric": map[string]interface{}{
						"type":        "string",
						"description": "Metric name to look up (optional; if omitted, returns all metadata)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of metrics to return when no metric name is given (default: 50)",
					},
				},
			},
		},
	}, nil
}

// CallTool dispatches a tool call to the appropriate handler.
func (s *Server) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	handler, ok := s.handlers[name]
	if !ok {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return handler(ctx, arguments)
}

// Shutdown cleans up resources.
func (s *Server) Shutdown(ctx context.Context) error {
	return nil
}

// --- Tool handlers ---

func (s *Server) handleQuery(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := arguments["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}

	params := url.Values{}
	params.Set("query", query)
	if t, ok := arguments["time"].(string); ok && t != "" {
		params.Set("time", t)
	}
	if timeout, ok := arguments["timeout"].(string); ok && timeout != "" {
		params.Set("timeout", timeout)
	}

	apiURL := s.cfg.BaseURL + "/api/v1/query?" + params.Encode()
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("query failed: %s", err.Error())), nil
	}

	var data promData
	if err := json.Unmarshal(rawData, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse query data: %s", err.Error())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Result type: %s\n\n", data.ResultType))

	switch data.ResultType {
	case "vector":
		var samples []vectorSample
		if err := json.Unmarshal(data.Result, &samples); err != nil {
			return mcpserver.ErrorResult(fmt.Sprintf("parse vector result: %s", err.Error())), nil
		}
		if len(samples) == 0 {
			sb.WriteString("No data.\n")
		}
		for _, sample := range samples {
			metricName := sample.Metric["__name__"]
			labels := formatLabels(sample.Metric, true)
			ts := parseTimestamp(sample.Value[0])
			val := parseValue(sample.Value[1])
			if metricName != "" {
				sb.WriteString(fmt.Sprintf("%s%s => %s @%s\n", metricName, labels, val, ts))
			} else {
				sb.WriteString(fmt.Sprintf("%s => %s @%s\n", labels, val, ts))
			}
		}
	case "scalar":
		var v [2]json.RawMessage
		if err := json.Unmarshal(data.Result, &v); err != nil {
			return mcpserver.ErrorResult(fmt.Sprintf("parse scalar result: %s", err.Error())), nil
		}
		ts := parseTimestamp(v[0])
		val := parseValue(v[1])
		sb.WriteString(fmt.Sprintf("%s @%s\n", val, ts))
	default:
		sb.WriteString(fmt.Sprintf("Raw result: %s\n", string(data.Result)))
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleQueryRange(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	query, ok := arguments["query"].(string)
	if !ok || query == "" {
		return mcpserver.ErrorResult("query is required"), nil
	}
	start, ok := arguments["start"].(string)
	if !ok || start == "" {
		return mcpserver.ErrorResult("start is required"), nil
	}
	end, ok := arguments["end"].(string)
	if !ok || end == "" {
		return mcpserver.ErrorResult("end is required"), nil
	}
	step, ok := arguments["step"].(string)
	if !ok || step == "" {
		return mcpserver.ErrorResult("step is required"), nil
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start)
	params.Set("end", end)
	params.Set("step", step)
	if timeout, ok := arguments["timeout"].(string); ok && timeout != "" {
		params.Set("timeout", timeout)
	}

	apiURL := s.cfg.BaseURL + "/api/v1/query_range?" + params.Encode()
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("range query failed: %s", err.Error())), nil
	}

	var data promData
	if err := json.Unmarshal(rawData, &data); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse range query data: %s", err.Error())), nil
	}

	var series []matrixSample
	if err := json.Unmarshal(data.Result, &series); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse matrix result: %s", err.Error())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Range query: %s\n", query))
	sb.WriteString(fmt.Sprintf("Time range: %s to %s, step: %s\n", start, end, step))
	sb.WriteString(fmt.Sprintf("Total series: %d\n\n", len(series)))

	for i, s2 := range series {
		metricName := s2.Metric["__name__"]
		labels := formatLabels(s2.Metric, true)
		if metricName != "" {
			sb.WriteString(fmt.Sprintf("Series %d: %s%s\n", i+1, metricName, labels))
		} else {
			sb.WriteString(fmt.Sprintf("Series %d: %s\n", i+1, labels))
		}

		pts := s2.Values
		n := len(pts)
		if n == 0 {
			sb.WriteString("  (no data points)\n")
		} else if n <= 50 {
			for _, pt := range pts {
				ts := parseTimestamp(pt[0])
				val := parseValue(pt[1])
				sb.WriteString(fmt.Sprintf("  %s => %s\n", ts, val))
			}
		} else {
			// Show first 5, ellipsis, last 5
			for _, pt := range pts[:5] {
				ts := parseTimestamp(pt[0])
				val := parseValue(pt[1])
				sb.WriteString(fmt.Sprintf("  %s => %s\n", ts, val))
			}
			sb.WriteString(fmt.Sprintf("  ... %d more ...\n", n-10))
			for _, pt := range pts[n-5:] {
				ts := parseTimestamp(pt[0])
				val := parseValue(pt[1])
				sb.WriteString(fmt.Sprintf("  %s => %s\n", ts, val))
			}
			// Summary stats
			var floats []float64
			for _, pt := range pts {
				if f, err := strconv.ParseFloat(strings.Trim(string(pt[1]), `"`), 64); err == nil {
					floats = append(floats, f)
				}
			}
			if len(floats) > 0 {
				min, max, sum := floats[0], floats[0], 0.0
				for _, f := range floats {
					if f < min {
						min = f
					}
					if f > max {
						max = f
					}
					sum += f
				}
				avg := sum / float64(len(floats))
				sb.WriteString(fmt.Sprintf("  [min=%.6g max=%.6g avg=%.6g over %d points]\n", min, max, avg, n))
			}
		}
		sb.WriteString("\n")
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleListMetrics(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	match, _ := arguments["match"].(string)
	limit := 100
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	apiURL := s.cfg.BaseURL + "/api/v1/label/__name__/values"
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("list metrics failed: %s", err.Error())), nil
	}

	var names []string
	if err := json.Unmarshal(rawData, &names); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse metric names: %s", err.Error())), nil
	}

	// Filter client-side
	if match != "" {
		matchLower := strings.ToLower(match)
		var filtered []string
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), matchLower) {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}

	sort.Strings(names)
	total := len(names)
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}

	var sb strings.Builder
	for _, n := range names {
		sb.WriteString(n)
		sb.WriteString("\n")
	}
	if match != "" {
		sb.WriteString(fmt.Sprintf("\n(%d metrics matching %q", total, match))
	} else {
		sb.WriteString(fmt.Sprintf("\n(%d total metrics", total))
	}
	if total > limit {
		sb.WriteString(fmt.Sprintf(", showing first %d", limit))
	}
	sb.WriteString(")\n")

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetLabels(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	params := url.Values{}
	if match, ok := arguments["match"].(string); ok && match != "" {
		params.Add("match[]", match)
	}
	if start, ok := arguments["start"].(string); ok && start != "" {
		params.Set("start", start)
	}
	if end, ok := arguments["end"].(string); ok && end != "" {
		params.Set("end", end)
	}

	apiURL := s.cfg.BaseURL + "/api/v1/labels"
	if len(params) > 0 {
		apiURL += "?" + params.Encode()
	}

	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get labels failed: %s", err.Error())), nil
	}

	var labels []string
	if err := json.Unmarshal(rawData, &labels); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse labels: %s", err.Error())), nil
	}

	sort.Strings(labels)

	var sb strings.Builder
	for _, l := range labels {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n(%d labels)\n", len(labels)))

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetLabelValues(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	label, ok := arguments["label"].(string)
	if !ok || label == "" {
		return mcpserver.ErrorResult("label is required"), nil
	}

	limit := 100
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	apiURL := s.cfg.BaseURL + "/api/v1/label/" + url.PathEscape(label) + "/values"
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get label values failed: %s", err.Error())), nil
	}

	var values []string
	if err := json.Unmarshal(rawData, &values); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse label values: %s", err.Error())), nil
	}

	sort.Strings(values)
	total := len(values)
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Label: %s\n\n", label))
	for _, v := range values {
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n(%d values", total))
	if total > limit {
		sb.WriteString(fmt.Sprintf(", showing first %d", limit))
	}
	sb.WriteString(")\n")

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleFindSeries(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	match, ok := arguments["match"].(string)
	if !ok || match == "" {
		return mcpserver.ErrorResult("match is required"), nil
	}

	limit := 50
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	params := url.Values{}
	params.Add("match[]", match)
	if start, ok := arguments["start"].(string); ok && start != "" {
		params.Set("start", start)
	}
	if end, ok := arguments["end"].(string); ok && end != "" {
		params.Set("end", end)
	}

	apiURL := s.cfg.BaseURL + "/api/v1/series?" + params.Encode()
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("find series failed: %s", err.Error())), nil
	}

	var seriesList []map[string]string
	if err := json.Unmarshal(rawData, &seriesList); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse series: %s", err.Error())), nil
	}

	total := len(seriesList)
	if limit > 0 && len(seriesList) > limit {
		seriesList = seriesList[:limit]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Selector: %s\n\n", match))
	for _, m := range seriesList {
		sb.WriteString(formatLabels(m, false))
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("\n(%d series", total))
	if total > limit {
		sb.WriteString(fmt.Sprintf(", showing first %d", limit))
	}
	sb.WriteString(")\n")

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetTargets(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	state := "active"
	if st, ok := arguments["state"].(string); ok && st != "" {
		state = st
	}
	jobFilter, _ := arguments["job"].(string)

	apiURL := s.cfg.BaseURL + "/api/v1/targets"
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get targets failed: %s", err.Error())), nil
	}

	var targetsData struct {
		ActiveTargets []struct {
			Labels          map[string]string `json:"labels"`
			ScrapeURL       string            `json:"scrapeUrl"`
			LastScrape      string            `json:"lastScrape"`
			LastScrapeDuration float64        `json:"lastScrapeDuration"`
			LastError       string            `json:"lastError"`
			Health          string            `json:"health"`
		} `json:"activeTargets"`
		DroppedTargets []struct {
			DiscoveredLabels map[string]string `json:"discoveredLabels"`
		} `json:"droppedTargets"`
	}

	if err := json.Unmarshal(rawData, &targetsData); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse targets: %s", err.Error())), nil
	}

	var sb strings.Builder

	if state == "dropped" {
		targets := targetsData.DroppedTargets
		sb.WriteString(fmt.Sprintf("Dropped targets: %d\n\n", len(targets)))
		for i, t := range targets {
			sb.WriteString(fmt.Sprintf("Target %d:\n", i+1))
			keys := make([]string, 0, len(t.DiscoveredLabels))
			for k := range t.DiscoveredLabels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				sb.WriteString(fmt.Sprintf("  %s=%q\n", k, t.DiscoveredLabels[k]))
			}
			sb.WriteString("\n")
		}
	} else {
		targets := targetsData.ActiveTargets
		// Filter by job
		if jobFilter != "" {
			var filtered []struct {
				Labels          map[string]string `json:"labels"`
				ScrapeURL       string            `json:"scrapeUrl"`
				LastScrape      string            `json:"lastScrape"`
				LastScrapeDuration float64        `json:"lastScrapeDuration"`
				LastError       string            `json:"lastError"`
				Health          string            `json:"health"`
			}
			for _, t := range targets {
				if t.Labels["job"] == jobFilter {
					filtered = append(filtered, t)
				}
			}
			targets = filtered
		}

		sb.WriteString(fmt.Sprintf("Active targets: %d\n\n", len(targets)))
		for i, t := range targets {
			job := t.Labels["job"]
			instance := t.Labels["instance"]
			health := formatHealth(t.Health)
			sb.WriteString(fmt.Sprintf("Target %d: job=%s instance=%s\n", i+1, job, instance))
			sb.WriteString(fmt.Sprintf("  Health:      %s\n", health))
			sb.WriteString(fmt.Sprintf("  URL:         %s\n", t.ScrapeURL))
			if t.LastScrape != "" {
				sb.WriteString(fmt.Sprintf("  Last scrape: %s\n", t.LastScrape))
			}
			if t.LastError != "" {
				sb.WriteString(fmt.Sprintf("  Last error:  %s\n", t.LastError))
			}
			sb.WriteString("\n")
		}
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetAlerts(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	apiURL := s.cfg.BaseURL + "/api/v1/alerts"
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get alerts failed: %s", err.Error())), nil
	}

	var alertsData struct {
		Alerts []struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			State       string            `json:"state"`
			ActiveAt    string            `json:"activeAt"`
			Value       string            `json:"value"`
		} `json:"alerts"`
	}

	if err := json.Unmarshal(rawData, &alertsData); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse alerts: %s", err.Error())), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Alerts: %d\n\n", len(alertsData.Alerts)))

	for i, alert := range alertsData.Alerts {
		alertName := alert.Labels["alertname"]
		sb.WriteString(fmt.Sprintf("Alert %d: %s\n", i+1, alertName))
		sb.WriteString(fmt.Sprintf("  State:    %s\n", alert.State))
		if alert.ActiveAt != "" {
			sb.WriteString(fmt.Sprintf("  ActiveAt: %s\n", alert.ActiveAt))
		}
		if len(alert.Labels) > 0 {
			sb.WriteString("  Labels:\n")
			keys := make([]string, 0, len(alert.Labels))
			for k := range alert.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k != "alertname" {
					sb.WriteString(fmt.Sprintf("    %s=%q\n", k, alert.Labels[k]))
				}
			}
		}
		if len(alert.Annotations) > 0 {
			sb.WriteString("  Annotations:\n")
			keys := make([]string, 0, len(alert.Annotations))
			for k := range alert.Annotations {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				sb.WriteString(fmt.Sprintf("    %s: %s\n", k, alert.Annotations[k]))
			}
		}
		if alert.Value != "" {
			sb.WriteString(fmt.Sprintf("  Value:    %s\n", alert.Value))
		}
		sb.WriteString("\n")
	}

	if len(alertsData.Alerts) == 0 {
		sb.WriteString("No alerts.\n")
	}

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleGetRules(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	typeFilter, _ := arguments["type"].(string)
	nameFilter, _ := arguments["rule_name"].(string)
	nameFilterLower := strings.ToLower(nameFilter)

	apiURL := s.cfg.BaseURL + "/api/v1/rules"
	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get rules failed: %s", err.Error())), nil
	}

	var rulesData struct {
		Groups []struct {
			Name  string `json:"name"`
			File  string `json:"file"`
			Rules []struct {
				Type        string            `json:"type"`
				Name        string            `json:"name"`
				Query       string            `json:"query"`
				Duration    float64           `json:"duration"`
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
				// Alert rule fields
				Expr  string `json:"expr"`
				State string `json:"state"`
				// Recording rule fields
				// expr is already included above
			} `json:"rules"`
		} `json:"groups"`
	}

	if err := json.Unmarshal(rawData, &rulesData); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse rules: %s", err.Error())), nil
	}

	var sb strings.Builder
	totalRules := 0

	for _, group := range rulesData.Groups {
		groupWritten := false
		for _, rule := range group.Rules {
			// Apply type filter
			if typeFilter != "" && rule.Type != typeFilter {
				continue
			}
			// Apply name filter
			if nameFilterLower != "" && !strings.Contains(strings.ToLower(rule.Name), nameFilterLower) {
				continue
			}

			if !groupWritten {
				sb.WriteString(fmt.Sprintf("Group: %s (file: %s)\n", group.Name, group.File))
				groupWritten = true
			}

			totalRules++
			if rule.Type == "alerting" {
				sb.WriteString(fmt.Sprintf("  [alerting] %s\n", rule.Name))
				expr := rule.Expr
				if expr == "" {
					expr = rule.Query
				}
				sb.WriteString(fmt.Sprintf("    expr:     %s\n", expr))
				if rule.Duration > 0 {
					sb.WriteString(fmt.Sprintf("    duration: %gs\n", rule.Duration))
				}
				if rule.State != "" {
					sb.WriteString(fmt.Sprintf("    state:    %s\n", rule.State))
				}
				if len(rule.Labels) > 0 {
					sb.WriteString("    labels:   ")
					sb.WriteString(formatLabels(rule.Labels, false))
					sb.WriteString("\n")
				}
				if len(rule.Annotations) > 0 {
					sb.WriteString("    annotations:\n")
					keys := make([]string, 0, len(rule.Annotations))
					for k := range rule.Annotations {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						sb.WriteString(fmt.Sprintf("      %s: %s\n", k, rule.Annotations[k]))
					}
				}
			} else {
				sb.WriteString(fmt.Sprintf("  [recording] %s\n", rule.Name))
				expr := rule.Expr
				if expr == "" {
					expr = rule.Query
				}
				sb.WriteString(fmt.Sprintf("    expr: %s\n", expr))
			}
		}
		if groupWritten {
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("(%d rules", totalRules))
	if typeFilter != "" {
		sb.WriteString(fmt.Sprintf(" of type %q", typeFilter))
	}
	if nameFilter != "" {
		sb.WriteString(fmt.Sprintf(" matching %q", nameFilter))
	}
	sb.WriteString(")\n")

	return mcpserver.TextResult(sb.String()), nil
}

func (s *Server) handleMetricMetadata(ctx context.Context, arguments map[string]interface{}) (*mcpserver.CallToolResult, error) {
	limit := 50
	if raw, ok := arguments["limit"]; ok {
		if n := toInt(raw); n > 0 {
			limit = n
		}
	}

	params := url.Values{}
	metricName, hasMetric := arguments["metric"].(string)
	if hasMetric && metricName != "" {
		params.Set("metric", metricName)
	}

	apiURL := s.cfg.BaseURL + "/api/v1/metadata"
	if len(params) > 0 {
		apiURL += "?" + params.Encode()
	}

	rawData, err := s.doGet(ctx, apiURL)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("get metadata failed: %s", err.Error())), nil
	}

	// metadata response: map[metricName][]metadataEntry
	var metadataMap map[string][]struct {
		Type string `json:"type"`
		Help string `json:"help"`
		Unit string `json:"unit"`
	}
	if err := json.Unmarshal(rawData, &metadataMap); err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("parse metadata: %s", err.Error())), nil
	}

	// Sort metric names
	names := make([]string, 0, len(metadataMap))
	for n := range metadataMap {
		names = append(names, n)
	}
	sort.Strings(names)

	total := len(names)
	if !hasMetric || metricName == "" {
		if limit > 0 && len(names) > limit {
			names = names[:limit]
		}
	}

	var sb strings.Builder
	for _, n := range names {
		entries := metadataMap[n]
		for _, entry := range entries {
			sb.WriteString(fmt.Sprintf("Metric: %s\n", n))
			sb.WriteString(fmt.Sprintf("  Type: %s\n", entry.Type))
			if entry.Help != "" {
				sb.WriteString(fmt.Sprintf("  Help: %s\n", entry.Help))
			}
			if entry.Unit != "" {
				sb.WriteString(fmt.Sprintf("  Unit: %s\n", entry.Unit))
			}
		}
	}

	if !hasMetric || metricName == "" {
		sb.WriteString(fmt.Sprintf("\n(%d metrics", total))
		if total > limit {
			sb.WriteString(fmt.Sprintf(", showing first %d", limit))
		}
		sb.WriteString(")\n")
	}

	return mcpserver.TextResult(sb.String()), nil
}

// --- Formatting helpers ---

// formatLabels formats a metric label set as {k="v", ...}.
// If omitName is true, the __name__ label is excluded from the braces
// (it should have already been shown as the metric name prefix).
func formatLabels(m map[string]string, omitName bool) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if omitName && k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// formatHealth formats a Prometheus target health string as UP/DOWN/UNKNOWN.
func formatHealth(h string) string {
	switch strings.ToLower(h) {
	case "up":
		return "UP"
	case "down":
		return "DOWN"
	default:
		return "UNKNOWN"
	}
}

// parseTimestamp parses a JSON-encoded Prometheus timestamp (Unix float) and
// returns it formatted as RFC3339. Falls back to the raw string on error.
func parseTimestamp(raw json.RawMessage) string {
	s := strings.Trim(string(raw), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	t := time.Unix(sec, nsec).UTC()
	return t.Format(time.RFC3339)
}

// parseValue extracts the string form of a Prometheus sample value.
func parseValue(raw json.RawMessage) string {
	return strings.Trim(string(raw), `"`)
}

// --- Result helpers ---



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
