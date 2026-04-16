package builtin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCalculatorTool_Add(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "add", "a": 5, "b": 3}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got error: %s", result.Error)
	}

	if result.Content != "8" {
		t.Errorf("Expected result '8', got '%s'", result.Content)
	}
}

func TestCalculatorTool_Subtract(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "subtract", "a": 10, "b": 4}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got error: %s", result.Error)
	}

	if result.Content != "6" {
		t.Errorf("Expected result '6', got '%s'", result.Content)
	}
}

func TestCalculatorTool_Multiply(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "multiply", "a": 7, "b": 6}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got error: %s", result.Error)
	}

	if result.Content != "42" {
		t.Errorf("Expected result '42', got '%s'", result.Content)
	}
}

func TestCalculatorTool_Divide(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "divide", "a": 20, "b": 4}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success, got error: %s", result.Error)
	}

	if result.Content != "5" {
		t.Errorf("Expected result '5', got '%s'", result.Content)
	}
}

func TestCalculatorTool_DivideByZero(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "divide", "a": 10, "b": 0}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Success {
		t.Error("Expected failure for division by zero")
	}

	if result.Error != "Division by zero" {
		t.Errorf("Expected 'Division by zero' error, got '%s'", result.Error)
	}
}

func TestCalculatorTool_InvalidOperation(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"operation": "modulo", "a": 10, "b": 3}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Success {
		t.Error("Expected failure for invalid operation")
	}
}

func TestCalculatorTool_InvalidParams(t *testing.T) {
	calc := NewCalculatorTool()

	params := `{"invalid": "json"}`
	result, err := calc.Execute(context.Background(), json.RawMessage(params))

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Success {
		t.Error("Expected failure for invalid parameters")
	}
}

func TestCalculatorTool_Schema(t *testing.T) {
	calc := NewCalculatorTool()

	schema := calc.Schema()

	if schema.Type != "object" {
		t.Errorf("Expected schema type 'object', got '%s'", schema.Type)
	}

	if len(schema.Required) != 3 {
		t.Errorf("Expected 3 required fields, got %d", len(schema.Required))
	}
}
