// dump-context prints the fully-rendered system prompt and (optionally) a
// conversation trace exactly as they would be sent to the LLM.  It uses the
// same app.BuildSystemPrompt path as the TUI and headless server — not a copy.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/mcp"
	"github.com/stanier/stoa/internal/skills"
	"github.com/stanier/stoa/internal/skills/builtin"
	"github.com/stanier/stoa/internal/trace"
)

func main() {
	jsonlPath := flag.String("jsonl", "", "path to a .jsonl trace file to render after the system prompt")
	persona := flag.String("persona", "", "persona name to use (overrides config)")
	noMCP := flag.Bool("no-mcp", false, "skip MCP server connection")
	mcpTimeout := flag.Duration("mcp-timeout", 15*time.Second, "MCP connect timeout")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Skills registry — mirror what server and TUI do.
	registry := skills.NewRegistry()
	for _, toolName := range cfg.Skills.EnabledBuiltin {
		if toolName == "calculator" {
			registry.RegisterTool(builtin.NewCalculatorTool())
		}
	}

	// Prompt registry — shared helper.
	promptRegistry := app.SetupPromptRegistry(cfg)

	// Persona registry — same pattern as server.
	personaRegistry := personas.NewRegistry()
	if configDir, err := config.GetConfigDir(); err == nil {
		if err := personaRegistry.LoadFromConfig(configDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to load personas: %v\n", err)
		}
	}

	// MCP tools — connect synchronously since this is a one-shot utility.
	if !*noMCP && cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mcpManager := mcp.NewServerManager(cfg.MCP)
		ctx, cancel := context.WithTimeout(context.Background(), *mcpTimeout)
		defer cancel()

		if err := mcpManager.ConnectAll(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: MCP connect failed: %v\n", err)
		} else if tools, err := mcpManager.DiscoverTools(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: MCP discover failed: %v\n", err)
		} else {
			for _, tool := range tools {
				registry.RegisterTool(tool)
			}
			fmt.Fprintf(os.Stderr, "info: registered %d MCP tools\n", len(tools))
		}
	}

	// Resolve persona name: flag > config.
	personaName := cfg.Zulip.Persona
	if *persona != "" {
		personaName = *persona
	}

	// Build system prompt via the exact same function used by TUI and server.
	llmCtx := app.BuildLLMContext(cfg, registry, promptRegistry, personaRegistry, personaName, nil)

	fmt.Print(llmCtx.SystemPrompt)

	if *jsonlPath != "" {
		events, err := loadTrace(*jsonlPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to read jsonl: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(renderTrace(events))
	}
}

// loadTrace reads all TraceEvent objects from a JSONL file.
func loadTrace(path string) ([]trace.TraceEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []trace.TraceEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MB line buffer
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev trace.TraceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping malformed line: %v\n", err)
			continue
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

// renderTrace formats a slice of trace events for human reading.
func renderTrace(events []trace.TraceEvent) string {
	if len(events) == 0 {
		return "(no events)"
	}

	var sb strings.Builder
	for i, ev := range events {
		ts := ev.Timestamp.Format("15:04:05")
		sb.WriteString(fmt.Sprintf("── [%d] %s  type=%-20s", i+1, ts, string(ev.Type)))
		if ev.Role != "" {
			sb.WriteString(fmt.Sprintf("  role=%s", ev.Role))
		}
		sb.WriteString("\n")

		switch ev.Type {
		case trace.EventSystemMessage:
			sb.WriteString(indentBlock(ev.Content, "   "))
		case trace.EventUserMessage, trace.EventAssistantMessage:
			if ev.Thinking != "" {
				sb.WriteString("   [thinking]\n")
				sb.WriteString(indentBlock(ev.Thinking, "   | "))
			}
			sb.WriteString(indentBlock(ev.Content, "   "))
		case trace.EventToolCall:
			sb.WriteString(fmt.Sprintf("   tool: %s  id=%s\n", ev.ToolName, ev.ToolCallID))
			if len(ev.ToolInput) > 0 {
				raw, _ := json.MarshalIndent(ev.ToolInput, "   ", "  ")
				sb.WriteString("   input: ")
				sb.Write(raw)
				sb.WriteByte('\n')
			}
		case trace.EventToolResult:
			sb.WriteString(fmt.Sprintf("   tool: %s  id=%s\n", ev.ToolName, ev.ToolCallID))
			sb.WriteString(indentBlock(ev.ToolOutput, "   "))
		case trace.EventError:
			sb.WriteString(fmt.Sprintf("   ERROR: %s\n", ev.Error))
		default:
			if ev.Content != "" {
				sb.WriteString(indentBlock(ev.Content, "   "))
			}
		}

		if i < len(events)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// indentBlock prefixes every line of s with the given prefix.
func indentBlock(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(prefix)
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}
