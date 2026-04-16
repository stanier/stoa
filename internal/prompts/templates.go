package prompts

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/stanier/stoa/internal/skills"
)

// TemplateData holds all available variables for template rendering
type TemplateData struct {
	// Context information
	Model       string
	Provider    string
	Date        string
	Time        string
	Environment string

	// Tools and Commands (rendered as markdown lists)
	Tools    string
	Commands string

	// Structured data for advanced templates
	ToolsList    []ToolInfo
	CommandsList []CommandInfo

	// Agent Skills XML block injected when skills are configured
	AgentSkills string

	// Additional context
	Features []string
	Custom   map[string]interface{}
}

// ToolInfo represents a tool for display in templates
type ToolInfo struct {
	Name        string
	Description string
	Parameters  string // JSON Schema as string
}

// CommandInfo represents a command for display in templates
type CommandInfo struct {
	Name        string
	Description string
	Usage       string
}

// BuildTemplateDataFromSlices creates TemplateData from pre-filtered tool and command slices.
// Use this when you have already determined which tools are allowed (e.g. by persona) so that
// the system prompt text and the API tool definitions are built from the same filtered set.
func BuildTemplateDataFromSlices(tools []skills.Tool, commands []skills.Command, model, provider string) *TemplateData {
	now := time.Now()
	return &TemplateData{
		Model:        model,
		Provider:     provider,
		Date:         now.Format("2006-01-02"),
		Time:         now.Format("15:04:05"),
		Environment:  "Stoa Application",
		Tools:        formatToolSlice(tools),
		Commands:     formatCommandSlice(commands),
		ToolsList:    toolInfoSlice(tools),
		CommandsList: commandInfoSlice(commands),
		Features: []string{
			"Conversation Compaction",
			"Sub-Agents",
			"MCP Servers",
			"Multi-Channel Support",
		},
		Custom: make(map[string]interface{}),
	}
}

// BuildTemplateData creates a complete TemplateData object with all available context
func BuildTemplateData(skillsReg *skills.Registry, model, provider string) *TemplateData {
	now := time.Now()

	data := &TemplateData{
		Model:        model,
		Provider:     provider,
		Date:         now.Format("2006-01-02"),
		Time:         now.Format("15:04:05"),
		Environment:  "Stoa Application",
		Tools:        formatTools(skillsReg),
		Commands:     formatCommands(skillsReg),
		ToolsList:    extractToolInfo(skillsReg),
		CommandsList: extractCommandInfo(skillsReg),
		Features: []string{
			"Conversation Compaction",
			"Sub-Agents",
			"MCP Servers",
			"Multi-Channel Support",
		},
		Custom: make(map[string]interface{}),
	}

	return data
}

// formatTools returns tools formatted as a markdown list
func formatTools(reg *skills.Registry) string {
	if reg == nil {
		return "No tools available"
	}

	tools := reg.ListTools()
	if len(tools) == 0 {
		return "No tools available"
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")

	for _, tool := range tools {
		name := tool.Name()
		desc := tool.Description()

		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", name, desc))
	}

	return sb.String()
}

// formatCommands returns commands formatted as a markdown list
func formatCommands(reg *skills.Registry) string {
	if reg == nil {
		return "No commands available"
	}

	commands := reg.ListCommands()
	if len(commands) == 0 {
		return "No commands available"
	}

	var sb strings.Builder
	sb.WriteString("## Available Commands\n\n")

	for _, cmd := range commands {
		name := cmd.Name()
		desc := cmd.Description()

		sb.WriteString(fmt.Sprintf("- **/%s**: %s\n", name, desc))
	}

	return sb.String()
}

// extractToolInfo extracts tool information for structured access
func extractToolInfo(reg *skills.Registry) []ToolInfo {
	if reg == nil {
		return []ToolInfo{}
	}

	tools := reg.ListTools()
	infos := make([]ToolInfo, 0, len(tools))

	for _, tool := range tools {
		schema := tool.Schema()
		schemaStr, _ := marshalSchema(schema)

		infos = append(infos, ToolInfo{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schemaStr,
		})
	}

	return infos
}

// extractCommandInfo extracts command information for structured access
func extractCommandInfo(reg *skills.Registry) []CommandInfo {
	if reg == nil {
		return []CommandInfo{}
	}

	commands := reg.ListCommands()
	infos := make([]CommandInfo, 0, len(commands))

	for _, cmd := range commands {
		infos = append(infos, CommandInfo{
			Name:        cmd.Name(),
			Description: cmd.Description(),
			Usage:       cmd.Usage(),
		})
	}

	return infos
}

// marshalSchema converts a ToolSchema to a string representation
func marshalSchema(schema skills.ToolSchema) (string, error) {
	if schema.Type == "" {
		return "{}", nil
	}

	var sb strings.Builder
	sb.WriteString("{\n")
	sb.WriteString(fmt.Sprintf("  \"type\": \"%s\"", schema.Type))

	if len(schema.Properties) > 0 {
		sb.WriteString(",\n  \"properties\": {\n")
		props := schema.Properties
		count := 0
		for key, val := range props {
			if count > 0 {
				sb.WriteString(",\n")
			}
			sb.WriteString(fmt.Sprintf("    \"%s\": %v", key, val))
			count++
		}
		sb.WriteString("\n  }")
	}

	if len(schema.Required) > 0 {
		sb.WriteString(",\n  \"required\": [")
		for i, req := range schema.Required {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("\"%s\"", req))
		}
		sb.WriteString("]")
	}

	sb.WriteString("\n}")
	return sb.String(), nil
}

// SimpleTemplateData creates a minimal TemplateData with just the basics
// Useful when skills registry is not available
func SimpleTemplateData(model, provider string) *TemplateData {
	return BuildTemplateData(nil, model, provider)
}

func formatToolSlice(tools []skills.Tool) string {
	if len(tools) == 0 {
		return "No tools available"
	}
	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Name(), t.Description()))
	}
	return sb.String()
}

func formatCommandSlice(commands []skills.Command) string {
	if len(commands) == 0 {
		return "No commands available"
	}
	var sb strings.Builder
	sb.WriteString("## Available Commands\n\n")
	for _, c := range commands {
		sb.WriteString(fmt.Sprintf("- **/%s**: %s\n", c.Name(), c.Description()))
	}
	return sb.String()
}

func toolInfoSlice(tools []skills.Tool) []ToolInfo {
	infos := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		schemaStr, _ := marshalSchema(t.Schema())
		infos = append(infos, ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  schemaStr,
		})
	}
	return infos
}

func commandInfoSlice(commands []skills.Command) []CommandInfo {
	infos := make([]CommandInfo, 0, len(commands))
	for _, c := range commands {
		infos = append(infos, CommandInfo{
			Name:        c.Name(),
			Description: c.Description(),
			Usage:       c.Usage(),
		})
	}
	return infos
}

// RenderTemplate renders a template string with the given data
// This is a standalone function that can be used outside of the Registry
func RenderTemplate(templateStr string, data interface{}) (string, error) {
	tmpl, err := template.New("template").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
