package agentskills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stanier/stoa/internal/skills"
)

// LoadSkillTool implements skills.Tool for the "load_skill" built-in.
// It returns the full Markdown body of a named skill's SKILL.md.
type LoadSkillTool struct {
	skillList []*SkillMetadata
}

// NewLoadSkillTool creates a LoadSkillTool with the given discovered skills.
func NewLoadSkillTool(skillList []*SkillMetadata) *LoadSkillTool {
	return &LoadSkillTool{skillList: skillList}
}

func (t *LoadSkillTool) Name() string { return "load_skill" }

func (t *LoadSkillTool) Description() string {
	return "Load the full instructions for an Agent Skill by name. Use this when a task matches a skill's description."
}

func (t *LoadSkillTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"skill_name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the skill to load (as listed in available_skills)",
			},
		},
		Required: []string{"skill_name"},
	}
}

func (t *LoadSkillTool) Execute(_ context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		SkillName string `json:"skill_name"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return skills.ToolResult{Success: false, Error: fmt.Sprintf("invalid params: %v", err)}, nil
	}

	meta := t.findSkill(args.SkillName)
	if meta == nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("skill %q not found", args.SkillName),
		}, nil
	}

	body, err := BodyOf(meta.Path)
	if err != nil {
		return skills.ToolResult{Success: false, Error: err.Error()}, nil
	}

	return skills.ToolResult{Success: true, Content: body}, nil
}

func (t *LoadSkillTool) findSkill(name string) *SkillMetadata {
	for _, s := range t.skillList {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// LoadSkillFileTool implements skills.Tool for the "load_skill_file" built-in.
// It reads a file from a skill's references/ or assets/ directory.
type LoadSkillFileTool struct {
	skillList []*SkillMetadata
}

// NewLoadSkillFileTool creates a LoadSkillFileTool with the given discovered skills.
func NewLoadSkillFileTool(skillList []*SkillMetadata) *LoadSkillFileTool {
	return &LoadSkillFileTool{skillList: skillList}
}

func (t *LoadSkillFileTool) Name() string { return "load_skill_file" }

func (t *LoadSkillFileTool) Description() string {
	return "Load a file from an Agent Skill's references/ or assets/ directory."
}

func (t *LoadSkillFileTool) Schema() skills.ToolSchema {
	return skills.ToolSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"skill_name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the skill",
			},
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "Path relative to the skill root, e.g. references/REFERENCE.md",
			},
		},
		Required: []string{"skill_name", "file_path"},
	}
}

func (t *LoadSkillFileTool) Execute(_ context.Context, params json.RawMessage) (skills.ToolResult, error) {
	var args struct {
		SkillName string `json:"skill_name"`
		FilePath  string `json:"file_path"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return skills.ToolResult{Success: false, Error: fmt.Sprintf("invalid params: %v", err)}, nil
	}

	meta := t.findSkill(args.SkillName)
	if meta == nil {
		return skills.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("skill %q not found", args.SkillName),
		}, nil
	}

	// Path traversal guard: reject any component that is ".."
	cleanPath := filepath.Clean(args.FilePath)
	for _, part := range strings.Split(cleanPath, string(filepath.Separator)) {
		if part == ".." {
			return skills.ToolResult{
				Success: false,
				Error:   "file_path must not contain '..' components",
			}, nil
		}
	}

	absPath := filepath.Join(meta.Path, cleanPath)

	// Double-check that the resolved path is still inside the skill directory.
	if !strings.HasPrefix(absPath, meta.Path+string(filepath.Separator)) &&
		absPath != meta.Path {
		return skills.ToolResult{
			Success: false,
			Error:   "file_path escapes the skill directory",
		}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return skills.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("file not found: %s", args.FilePath),
			}, nil
		}
		return skills.ToolResult{Success: false, Error: err.Error()}, nil
	}

	return skills.ToolResult{Success: true, Content: string(data)}, nil
}

func (t *LoadSkillFileTool) findSkill(name string) *SkillMetadata {
	for _, s := range t.skillList {
		if s.Name == name {
			return s
		}
	}
	return nil
}
