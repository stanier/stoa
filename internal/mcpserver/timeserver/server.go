package timeserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
)

// Server implements the MCP time server. It is stateless — all operations
// use only stdlib time functions and require no configuration.
type Server struct {
	mcpserver.BaseServer
	handlers map[string]mcpserver.ToolHandler
}

// NewServer creates a new time MCP server.
func NewServer(name, version string) *Server {
	s := &Server{
		BaseServer: mcpserver.BaseServer{Name: name, Version: version},
		handlers:   make(map[string]mcpserver.ToolHandler),
	}
	s.handlers["current_time"] = s.handleCurrentTime
	s.handlers["parse_date"] = s.handleParseDate
	s.handlers["format_date"] = s.handleFormatDate
	s.handlers["date_diff"] = s.handleDateDiff
	s.handlers["add_duration"] = s.handleAddDuration
	s.handlers["timezone_convert"] = s.handleTimezoneConvert
	s.handlers["business_days"] = s.handleBusinessDays
	return s
}

func (s *Server) ListTools(_ context.Context) ([]mcpserver.Tool, error) {
	return []mcpserver.Tool{
		{
			Name:        "current_time",
			Description: "Get the current date and time, optionally in a specific timezone.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": `IANA timezone name (e.g. "America/New_York", "Europe/London"). Defaults to UTC.`,
					},
				},
			},
		},
		{
			Name:        "parse_date",
			Description: "Parse a date string in a common format and return an RFC3339 timestamp. Tries multiple formats automatically.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date_string": map[string]interface{}{
						"type":        "string",
						"description": `Date string to parse (e.g. "2026-01-15", "Jan 15, 2026", "01/15/2026", "2026-01-15T14:30:00Z")`,
					},
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "Timezone to assume when the input has no timezone info. Defaults to UTC.",
					},
				},
				"required": []string{"date_string"},
			},
		},
		{
			Name:        "format_date",
			Description: `Format an RFC3339 date string. Named formats: "rfc3339", "date" (YYYY-MM-DD), "datetime" (YYYY-MM-DD HH:MM:SS), "human" (Monday, January 2, 2006 at 3:04 PM), "time" (HH:MM:SS). Any other value is treated as a Go time layout string.`,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "RFC3339 date string to format",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"description": `Named format ("rfc3339", "date", "datetime", "human", "time") or Go time layout string`,
					},
					"timezone": map[string]interface{}{
						"type":        "string",
						"description": "IANA timezone for display. Defaults to the timezone embedded in the input.",
					},
				},
				"required": []string{"date", "format"},
			},
		},
		{
			Name:        "date_diff",
			Description: "Calculate the signed difference between two dates.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]interface{}{
						"type":        "string",
						"description": "Start date (RFC3339 or common date string)",
					},
					"to": map[string]interface{}{
						"type":        "string",
						"description": "End date (RFC3339 or common date string)",
					},
					"unit": map[string]interface{}{
						"type":        "string",
						"description": `Unit for the result: "days", "hours", "minutes", "seconds". Defaults to "days".`,
						"enum":        []string{"days", "hours", "minutes", "seconds"},
					},
				},
				"required": []string{"from", "to"},
			},
		},
		{
			Name:        "add_duration",
			Description: `Add (or subtract) a duration from a date. Supports Go durations ("24h", "90m") and human units: "7d", "2 weeks", "-1 month", "1 year".`,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "Base date (RFC3339 or common date string)",
					},
					"duration": map[string]interface{}{
						"type":        "string",
						"description": `Duration to add, e.g. "7d", "2 weeks", "-1 month", "24h", "90m"`,
					},
				},
				"required": []string{"date", "duration"},
			},
		},
		{
			Name:        "timezone_convert",
			Description: "Convert a date/time from one timezone to another.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"date": map[string]interface{}{
						"type":        "string",
						"description": "Date/time to convert (RFC3339 or common date string)",
					},
					"to_timezone": map[string]interface{}{
						"type":        "string",
						"description": `Target IANA timezone name (e.g. "America/Los_Angeles", "Asia/Tokyo")`,
					},
					"from_timezone": map[string]interface{}{
						"type":        "string",
						"description": "Source timezone, used if the input has no timezone info. Defaults to UTC.",
					},
				},
				"required": []string{"date", "to_timezone"},
			},
		},
		{
			Name:        "business_days",
			Description: "Count business days between two dates, or add N business days to a date. Excludes weekends; does not account for holidays.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]interface{}{
						"type":        "string",
						"description": "Start date (RFC3339 or common date string)",
					},
					"to": map[string]interface{}{
						"type":        "string",
						"description": "End date. When provided, counts business days between 'from' and 'to'.",
					},
					"add": map[string]interface{}{
						"type":        "integer",
						"description": "Number of business days to add to 'from' (can be negative). Used when 'to' is not provided.",
					},
				},
				"required": []string{"from"},
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

func (s *Server) handleCurrentTime(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	tz := mcpserver.StrArg(args, "timezone", "UTC")
	loc, err := loadLocation(tz)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown timezone %q: %v", tz, err)), nil
	}
	now := time.Now().In(loc)
	return mcpserver.TextResult(fmt.Sprintf("%s\n%s",
		now.Format(time.RFC3339),
		now.Format("Monday, January 2, 2006 at 3:04:05 PM MST"),
	)), nil
}

func (s *Server) handleParseDate(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	input := mcpserver.StrArg(args, "date_string", "")
	if input == "" {
		return mcpserver.ErrorResult("date_string is required"), nil
	}
	tz := mcpserver.StrArg(args, "timezone", "UTC")
	loc, err := loadLocation(tz)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown timezone %q: %v", tz, err)), nil
	}
	t, err := parseDate(input, loc)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse %q: %v", input, err)), nil
	}
	return mcpserver.TextResult(t.Format(time.RFC3339)), nil
}

func (s *Server) handleFormatDate(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	dateStr := mcpserver.StrArg(args, "date", "")
	format := mcpserver.StrArg(args, "format", "")
	if dateStr == "" {
		return mcpserver.ErrorResult("date is required"), nil
	}
	if format == "" {
		return mcpserver.ErrorResult("format is required"), nil
	}
	t, err := parseDate(dateStr, time.UTC)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse date %q: %v", dateStr, err)), nil
	}
	if tz := mcpserver.StrArg(args, "timezone", ""); tz != "" {
		loc, err := loadLocation(tz)
		if err != nil {
			return mcpserver.ErrorResult(fmt.Sprintf("unknown timezone %q: %v", tz, err)), nil
		}
		t = t.In(loc)
	}
	return mcpserver.TextResult(t.Format(resolveFormat(format))), nil
}

func (s *Server) handleDateDiff(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	fromStr := mcpserver.StrArg(args, "from", "")
	toStr := mcpserver.StrArg(args, "to", "")
	unit := mcpserver.StrArg(args, "unit", "days")
	if fromStr == "" {
		return mcpserver.ErrorResult("from is required"), nil
	}
	if toStr == "" {
		return mcpserver.ErrorResult("to is required"), nil
	}
	from, err := parseDate(fromStr, time.UTC)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse 'from': %v", err)), nil
	}
	to, err := parseDate(toStr, time.UTC)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse 'to': %v", err)), nil
	}
	d := to.Sub(from)
	var val float64
	switch unit {
	case "hours":
		val = d.Hours()
	case "minutes":
		val = d.Minutes()
	case "seconds":
		val = d.Seconds()
	default:
		unit = "days"
		val = d.Hours() / 24
	}
	if val == float64(int64(val)) {
		return mcpserver.TextResult(fmt.Sprintf("%.0f %s", val, unit)), nil
	}
	return mcpserver.TextResult(fmt.Sprintf("%.2f %s", val, unit)), nil
}

func (s *Server) handleAddDuration(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	dateStr := mcpserver.StrArg(args, "date", "")
	durStr := mcpserver.StrArg(args, "duration", "")
	if dateStr == "" {
		return mcpserver.ErrorResult("date is required"), nil
	}
	if durStr == "" {
		return mcpserver.ErrorResult("duration is required"), nil
	}
	t, err := parseDate(dateStr, time.UTC)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse date %q: %v", dateStr, err)), nil
	}
	result, err := applyDuration(t, durStr)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse duration %q: %v", durStr, err)), nil
	}
	return mcpserver.TextResult(result.Format(time.RFC3339)), nil
}

func (s *Server) handleTimezoneConvert(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	dateStr := mcpserver.StrArg(args, "date", "")
	toTZ := mcpserver.StrArg(args, "to_timezone", "")
	fromTZ := mcpserver.StrArg(args, "from_timezone", "UTC")
	if dateStr == "" {
		return mcpserver.ErrorResult("date is required"), nil
	}
	if toTZ == "" {
		return mcpserver.ErrorResult("to_timezone is required"), nil
	}
	fromLoc, err := loadLocation(fromTZ)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown from_timezone %q: %v", fromTZ, err)), nil
	}
	toLoc, err := loadLocation(toTZ)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("unknown to_timezone %q: %v", toTZ, err)), nil
	}
	t, err := parseDate(dateStr, fromLoc)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse date: %v", err)), nil
	}
	out := t.In(toLoc)
	return mcpserver.TextResult(fmt.Sprintf("%s\n%s",
		out.Format(time.RFC3339),
		out.Format("Monday, January 2, 2006 at 3:04:05 PM MST"),
	)), nil
}

func (s *Server) handleBusinessDays(_ context.Context, args map[string]interface{}) (*mcpserver.CallToolResult, error) {
	fromStr := mcpserver.StrArg(args, "from", "")
	if fromStr == "" {
		return mcpserver.ErrorResult("from is required"), nil
	}
	from, err := parseDate(fromStr, time.UTC)
	if err != nil {
		return mcpserver.ErrorResult(fmt.Sprintf("could not parse 'from': %v", err)), nil
	}
	if toStr := mcpserver.StrArg(args, "to", ""); toStr != "" {
		to, err := parseDate(toStr, time.UTC)
		if err != nil {
			return mcpserver.ErrorResult(fmt.Sprintf("could not parse 'to': %v", err)), nil
		}
		return mcpserver.TextResult(fmt.Sprintf("%d business days", countBusinessDays(from, to))), nil
	}
	if addVal, ok := args["add"]; ok {
		n, ok := toInt(addVal)
		if !ok {
			return mcpserver.ErrorResult("add must be an integer"), nil
		}
		result := addBusinessDays(from, n)
		return mcpserver.TextResult(fmt.Sprintf("%s\n%s",
			result.Format(time.RFC3339),
			result.Format("Monday, January 2, 2006"),
		)), nil
	}
	return mcpserver.ErrorResult("provide 'to' to count business days, or 'add' to add business days"), nil
}

// --- helpers ---

func loadLocation(tz string) (*time.Location, error) {
	if tz == "" || strings.EqualFold(tz, "UTC") {
		return time.UTC, nil
	}
	return time.LoadLocation(tz)
}

var dateFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"01/02/2006",
	"01/02/2006 15:04:05",
	"January 2, 2006",
	"Jan 2, 2006",
	"2 January 2006",
	"2 Jan 2006",
}

func parseDate(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, f := range dateFormats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format (try RFC3339, e.g. 2006-01-02T15:04:05Z)")
}

func resolveFormat(f string) string {
	switch strings.ToLower(f) {
	case "rfc3339", "iso":
		return time.RFC3339
	case "date":
		return "2006-01-02"
	case "datetime":
		return "2006-01-02 15:04:05"
	case "human":
		return "Monday, January 2, 2006 at 3:04 PM MST"
	case "time":
		return "15:04:05"
	default:
		return f
	}
}

// applyDuration parses a human-readable duration string and adds it to t.
// Supports Go durations ("24h", "90m") and human units ("7d", "2 weeks", "1 month", "1y").
func applyDuration(t time.Time, s string) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	// Try Go's built-in duration parser first (handles "24h", "1h30m", etc.)
	if d, err := time.ParseDuration(s); err == nil {
		return t.Add(d), nil
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimSpace(s[1:])
	} else if strings.HasPrefix(s, "+") {
		s = strings.TrimSpace(s[1:])
	}
	// Find where the numeric prefix ends
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return t, fmt.Errorf("expected a number at start of duration")
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return t, err
	}
	if neg {
		n = -n
	}
	unit := strings.TrimSpace(s[i:])
	switch {
	case unit == "" || unit == "d" || strings.HasPrefix(unit, "day"):
		return t.AddDate(0, 0, n), nil
	case unit == "y" || strings.HasPrefix(unit, "year"):
		return t.AddDate(n, 0, 0), nil
	case unit == "mo" || strings.HasPrefix(unit, "month"):
		return t.AddDate(0, n, 0), nil
	case unit == "w" || strings.HasPrefix(unit, "week"):
		return t.AddDate(0, 0, n*7), nil
	case unit == "h" || strings.HasPrefix(unit, "hour"):
		return t.Add(time.Duration(n) * time.Hour), nil
	case unit == "m" || strings.HasPrefix(unit, "min"):
		return t.Add(time.Duration(n) * time.Minute), nil
	case unit == "s" || strings.HasPrefix(unit, "sec"):
		return t.Add(time.Duration(n) * time.Second), nil
	default:
		return t, fmt.Errorf("unknown unit %q (use y, mo, w, d, h, m, s)", unit)
	}
}

func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// countBusinessDays returns the signed count of business days from start to end.
// Start is inclusive, end is exclusive.
func countBusinessDays(start, end time.Time) int {
	if start.Equal(end) {
		return 0
	}
	neg := end.Before(start)
	if neg {
		start, end = end, start
	}
	count := 0
	for cur := start; cur.Before(end); cur = cur.AddDate(0, 0, 1) {
		if !isWeekend(cur) {
			count++
		}
	}
	if neg {
		return -count
	}
	return count
}

// addBusinessDays adds n business days to t, skipping weekends.
func addBusinessDays(t time.Time, n int) time.Time {
	if n == 0 {
		return t
	}
	step := 1
	if n < 0 {
		step = -1
		n = -n
	}
	cur := t
	for n > 0 {
		cur = cur.AddDate(0, 0, step)
		if !isWeekend(cur) {
			n--
		}
	}
	return cur
}


func toInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}


