package mcpserver

// StrArg extracts a string argument from an MCP tool arguments map.
// Returns def if the key is absent or not a string.
func StrArg(args map[string]interface{}, key, def string) string {
	v, ok := args[key]
	if !ok {
		return def
	}
	s, ok := v.(string)
	if !ok {
		return def
	}
	return s
}

// IntArg extracts an integer argument from an MCP tool arguments map.
// JSON numbers arrive as float64; int and int64 are also handled.
// Returns def if the key is absent or not a number.
func IntArg(args map[string]interface{}, key string, def int) int {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return def
}

// FloatArg extracts a float64 argument from an MCP tool arguments map.
// Returns def if the key is absent or not a number.
func FloatArg(args map[string]interface{}, key string, def float64) float64 {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return def
}

// BoolArg extracts a boolean argument from an MCP tool arguments map.
// Returns def if the key is absent or not a bool.
func BoolArg(args map[string]interface{}, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}
