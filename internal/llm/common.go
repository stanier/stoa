package llm

import (
	"encoding/json"

	"github.com/stanier/stoa/internal/logging"
)

// extractToolSchemaMap converts a tool's InputSchema to a map[string]interface{}.
// This is used by both Anthropic and OpenAI clients to normalize schema format.
// It handles both map and struct inputs by marshaling and unmarshaling through JSON.
func extractToolSchemaMap(inputSchema interface{}) map[string]interface{} {
	var schemaMap map[string]interface{}

	// Try to marshal and unmarshal to convert to map
	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		logging.Warnf("Failed to marshal tool schema: %v — tool will be registered with no parameter schema", err)
		return schemaMap
	}
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		logging.Warnf("Failed to unmarshal tool schema: %v — tool will be registered with no parameter schema", err)
	}

	return schemaMap
}

// extractSchemaProperties extracts the "properties" field from a schema map.
// Returns an empty map if the properties field is missing or has an unexpected type.
func extractSchemaProperties(schemaMap map[string]interface{}) map[string]interface{} {
	properties := make(map[string]interface{})
	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		properties = props
	}
	return properties
}
