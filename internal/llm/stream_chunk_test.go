package llm

import "testing"

// TestStreamChunkTokenFields verifies that StreamChunk carries InputTokens and
// OutputTokens on a Done chunk. This test will fail if those fields are ever
// accidentally removed from the struct.
func TestStreamChunkTokenFields(t *testing.T) {
	chunk := StreamChunk{
		Done:         true,
		InputTokens:  1234,
		OutputTokens: 567,
	}

	if !chunk.Done {
		t.Fatal("expected Done to be true")
	}
	if chunk.InputTokens != 1234 {
		t.Errorf("InputTokens: got %d, want 1234", chunk.InputTokens)
	}
	if chunk.OutputTokens != 567 {
		t.Errorf("OutputTokens: got %d, want 567", chunk.OutputTokens)
	}
}

// TestStreamChunkTokenFieldsZeroOnNonDone verifies that a non-Done chunk
// carries zero token counts by default.
func TestStreamChunkTokenFieldsZeroOnNonDone(t *testing.T) {
	chunk := StreamChunk{
		Content: "hello",
		Done:    false,
	}

	if chunk.InputTokens != 0 {
		t.Errorf("InputTokens on non-Done chunk: got %d, want 0", chunk.InputTokens)
	}
	if chunk.OutputTokens != 0 {
		t.Errorf("OutputTokens on non-Done chunk: got %d, want 0", chunk.OutputTokens)
	}
}

// TestStreamChunkDoneTokenTable is a table-driven test covering several
// representative token count pairs that a Done chunk might carry.
func TestStreamChunkDoneTokenTable(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
	}{
		{"both zero", 0, 0},
		{"typical small", 512, 128},
		{"large context", 100_000, 4096},
		{"only input", 800, 0},
		{"only output", 0, 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := StreamChunk{
				Done:         true,
				InputTokens:  tt.inputTokens,
				OutputTokens: tt.outputTokens,
			}
			if chunk.InputTokens != tt.inputTokens {
				t.Errorf("InputTokens: got %d, want %d", chunk.InputTokens, tt.inputTokens)
			}
			if chunk.OutputTokens != tt.outputTokens {
				t.Errorf("OutputTokens: got %d, want %d", chunk.OutputTokens, tt.outputTokens)
			}
		})
	}
}
