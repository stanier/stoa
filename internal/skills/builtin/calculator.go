package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/stanier/stoa/internal/skills"
)

// CalculatorTool provides basic arithmetic operations
type CalculatorTool struct{}

// NewCalculatorTool creates a new calculator tool
func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{}
}

// Name returns the tool name
func (t *CalculatorTool) Name() string {
	return "calculator"
}

// Description returns a human-readable description
func (t *CalculatorTool) Description() string {
	return "Perform basic arithmetic operations (add, subtract, multiply, divide)"
}

// Schema returns the JSON Schema for the tool parameters
func (t *CalculatorTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"description": "The operation to perform",
				"enum":        []string{"add", "subtract", "multiply", "divide"},
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "First operand",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "Second operand",
			},
		},
		Required: []string{"operation", "a", "b"},
	}
}

// Execute runs the tool with given parameters
func (t *CalculatorTool) Execute(ctx context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var input struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}

	if err := json.Unmarshal(params, &input); err != nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Invalid parameters: %v", err),
		}, nil
	}

	var result float64

	switch input.Operation {
	case "add":
		result = input.A + input.B
	case "subtract":
		result = input.A - input.B
	case "multiply":
		result = input.A * input.B
	case "divide":
		if input.B == 0 {
			return skills.ToolResult{
				Success: false,
				Error:   "Division by zero",
			}, nil
		}
		result = input.A / input.B
	default:
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Unknown operation: %s", input.Operation),
		}, nil
	}

	return skills.ToolResult{
		Success: true,
		Content: strconv.FormatFloat(result, 'f', -1, 64),
	}, nil
}
