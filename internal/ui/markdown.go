package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

// MarkdownRenderer provides utilities for rendering Markdown in the TUI
type MarkdownRenderer struct {
	glamourRenderer *glamour.TermRenderer
	mu              sync.RWMutex
	width           int
}

// NewMarkdownRenderer creates a new Markdown renderer with the given terminal width
func NewMarkdownRenderer(width int) (*MarkdownRenderer, error) {
	// Try different themes in order of preference
	themes := []string{"dark", "light"}
	var renderer *glamour.TermRenderer
	var err error

	for _, theme := range themes {
		renderer, err = glamour.NewTermRenderer(
			glamour.WithWordWrap(width),
			glamour.WithStylePath(theme),
		)
		if err == nil {
			break
		}
	}

	// If all themes fail, try without theme
	if err != nil {
		renderer, err = glamour.NewTermRenderer(
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return nil, err
		}
	}

	return &MarkdownRenderer{
		glamourRenderer: renderer,
		width:           width,
	}, nil
}

// Render renders Markdown content as terminal output
// For streaming content, incomplete Markdown (e.g., unclosed code blocks)
// is handled gracefully by the renderer
func (mr *MarkdownRenderer) Render(content string) string {
	mr.mu.RLock()
	renderer := mr.glamourRenderer
	mr.mu.RUnlock()

	if renderer == nil {
		return content
	}

	// Try to render as Markdown
	rendered, err := renderer.Render(content)
	if err != nil {
		// Fall back to plain text if rendering fails
		// This handles incomplete Markdown during streaming
		return content
	}

	trimmed := strings.TrimRight(rendered, "\n")

	// Debug: log if output changed
	if trimmed == content {
		// No transformation occurred
		return content
	}

	return trimmed
}

// UpdateWidth updates the rendering width (call when terminal is resized)
func (mr *MarkdownRenderer) UpdateWidth(width int) error {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	mr.width = width

	// Recreate renderer with new width
	renderer, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(width),
		glamour.WithStylePath("dark"),
	)
	if err != nil {
		// Try without theme if dark fails
		renderer, err = glamour.NewTermRenderer(
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return err
		}
	}

	mr.glamourRenderer = renderer
	return nil
}
