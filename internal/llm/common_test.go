package llm

import (
	"reflect"
	"testing"
)

func Test_extractToolSchemaMap(t *testing.T) {
	tests := []struct {
		name        string
		inputSchema interface{}
		want        map[string]interface{}
	}{
		{
			name: "simple schema map",
			inputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input": map[string]interface{}{
						"type":        "string",
						"description": "Test input",
					},
				},
			},
			want: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input": map[string]interface{}{
						"type":        "string",
						"description": "Test input",
					},
				},
			},
		},
		{
			name: "struct that needs marshaling",
			inputSchema: struct {
				Type       string                 `json:"type"`
				Properties map[string]interface{} `json:"properties"`
			}{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type": "string",
					},
				},
			},
			want: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
		{
			name:        "nil input",
			inputSchema: nil,
			want:        nil,
		},
		{
			name: "empty map",
			inputSchema: map[string]interface{}{},
			want: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolSchemaMap(tt.inputSchema)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractToolSchemaMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractSchemaProperties(t *testing.T) {
	tests := []struct {
		name      string
		schemaMap map[string]interface{}
		want      map[string]interface{}
	}{
		{
			name: "schema with properties",
			schemaMap: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type": "string",
					},
					"age": map[string]interface{}{
						"type": "number",
					},
				},
			},
			want: map[string]interface{}{
				"name": map[string]interface{}{
					"type": "string",
				},
				"age": map[string]interface{}{
					"type": "number",
				},
			},
		},
		{
			name: "schema without properties",
			schemaMap: map[string]interface{}{
				"type": "object",
			},
			want: map[string]interface{}{},
		},
		{
			name:      "nil schema",
			schemaMap: nil,
			want:      map[string]interface{}{},
		},
		{
			name:      "empty schema",
			schemaMap: map[string]interface{}{},
			want:      map[string]interface{}{},
		},
		{
			name: "properties field wrong type",
			schemaMap: map[string]interface{}{
				"type":       "object",
				"properties": "not a map",
			},
			want: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSchemaProperties(tt.schemaMap)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractSchemaProperties() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkExtractToolSchemaMap(b *testing.B) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"input": map[string]interface{}{
				"type":        "string",
				"description": "Test input",
			},
			"count": map[string]interface{}{
				"type":        "number",
				"description": "Count value",
			},
		},
		"required": []string{"input"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractToolSchemaMap(schema)
	}
}

func BenchmarkExtractSchemaProperties(b *testing.B) {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"age":  map[string]interface{}{"type": "number"},
			"email": map[string]interface{}{"type": "string"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractSchemaProperties(schemaMap)
	}
}
