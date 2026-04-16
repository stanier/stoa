// Package agentskills implements the Agent Skills (agentskills.io) tool-based integration.
// Skills are discovered from SKILL.md files in configured directories and exposed to the LLM
// via built-in tools (load_skill, load_skill_file) and an <available_skills> XML block in the
// system prompt.
package agentskills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stanier/stoa/internal/logging"
	"gopkg.in/yaml.v3"
)

// SkillMetadata holds the parsed front matter of a SKILL.md file.
// Path is populated at discovery time (not from the YAML itself).
type SkillMetadata struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility []string          `yaml:"compatibility,omitempty"`
	AllowedTools  []string          `yaml:"allowed_tools,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`

	// Path is the absolute path to the skill directory (set during discovery).
	Path string `yaml:"-"`
}

var validNameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ParseSkillMetadata reads SKILL.md from skillDir, parses the YAML front matter,
// and validates the metadata. skillDir is the directory containing SKILL.md.
func ParseSkillMetadata(skillDir string) (*SkillMetadata, error) {
	skillPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("reading SKILL.md: %w", err)
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("SKILL.md missing YAML front matter")
	}

	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("SKILL.md has malformed YAML front matter")
	}

	var meta SkillMetadata
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		return nil, fmt.Errorf("parsing YAML front matter: %w", err)
	}

	if meta.Name == "" {
		return nil, fmt.Errorf("skill name is required")
	}

	// Validate: name must match directory name
	dirName := filepath.Base(skillDir)
	if meta.Name != dirName {
		return nil, fmt.Errorf("skill name %q does not match directory name %q", meta.Name, dirName)
	}

	// Validate: name chars [a-z0-9-], no leading/trailing/consecutive hyphens
	if !validNameRe.MatchString(meta.Name) {
		return nil, fmt.Errorf("skill name %q is invalid (must match [a-z0-9]+(?:-[a-z0-9]+)*)", meta.Name)
	}

	meta.Path = skillDir
	return &meta, nil
}

// DiscoverSkills scans dirs for skill subdirectories containing SKILL.md.
// Missing directories are silently skipped. Invalid skills log a warning and are skipped.
func DiscoverSkills(dirs []string) ([]*SkillMetadata, error) {
	var skills []*SkillMetadata

	for _, dir := range dirs {
		dir = expandPath(dir)

		info, statErr := os.Stat(dir)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			logging.Warnf("agentskills: cannot stat %q: %v", dir, statErr)
			continue
		}
		if !info.IsDir() {
			logging.Warnf("agentskills: %q is not a directory, skipping", dir)
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			logging.Warnf("agentskills: cannot read directory %q: %v", dir, err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			skillDir := filepath.Join(dir, entry.Name())
			meta, err := ParseSkillMetadata(skillDir)
			if err != nil {
				logging.Warnf("agentskills: skipping %q: %v", skillDir, err)
				continue
			}

			skills = append(skills, meta)
		}
	}

	return skills, nil
}

// BodyOf reads SKILL.md from skillDir and returns the Markdown body after the
// closing --- of the front matter.
func BodyOf(skillDir string) (string, error) {
	skillPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("reading SKILL.md: %w", err)
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return strings.TrimSpace(content), nil
	}

	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) < 3 {
		return strings.TrimSpace(content), nil
	}

	return strings.TrimSpace(parts[2]), nil
}

// FormatXML returns an <available_skills>...</available_skills> XML block listing
// all discovered skills per the Agent Skills spec (tool-based profile, no <location>).
func FormatXML(skills []*SkillMetadata) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	for _, s := range skills {
		sb.WriteString("  <skill>\n")
		sb.WriteString(fmt.Sprintf("    <name>%s</name>\n", xmlEscape(s.Name)))
		sb.WriteString(fmt.Sprintf("    <description>%s</description>\n", xmlEscape(s.Description)))
		sb.WriteString("  </skill>\n")
	}
	sb.WriteString("</available_skills>")
	return sb.String()
}

// expandPath expands ~ to the user home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// xmlEscape escapes characters that are invalid inside XML text nodes.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
