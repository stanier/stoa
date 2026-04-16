package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanier/stoa/internal/permissions"
)

// PermissionPromptModel handles the UI for permission requests
type PermissionPromptModel struct {
	request *permissions.PermissionRequest

	// UI state
	focused  int  // 0 = Deny, 1 = Allow, 2 = Checkbox
	remember bool // Checkbox state for "remember choice"
	width    int
	height   int
}

// NewPermissionPromptModel creates a new permission prompt model
func NewPermissionPromptModel(request *permissions.PermissionRequest) *PermissionPromptModel {
	return &PermissionPromptModel{
		request:  request,
		focused:  1, // Default to "Allow"
		remember: false,
	}
}

// Init initializes the model
func (m *PermissionPromptModel) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *PermissionPromptModel) Update(msg tea.Msg) (*PermissionPromptModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			// Move focus forward
			m.focused = (m.focused + 1) % 3

		case "shift+tab":
			// Move focus backward
			m.focused = (m.focused + 2) % 3 // +2 is same as -1 in mod 3

		case "left", "up":
			// Move focus backward
			m.focused = (m.focused + 2) % 3

		case "right", "down":
			// Move focus forward
			m.focused = (m.focused + 1) % 3

		case " ":
			// If focused on checkbox, toggle it
			// Otherwise, allow space to toggle checkbox from anywhere
			if m.focused == 2 {
				m.remember = !m.remember
			} else {
				// Also allow toggling from buttons for convenience
				m.remember = !m.remember
			}

		case "enter":
			// If focused on checkbox, just toggle it
			if m.focused == 2 {
				m.remember = !m.remember
				return m, nil
			}

			// Send response based on which button is focused
			response := permissions.PermissionResponse{
				Approved:       m.focused == 1,
				RememberChoice: m.remember,
				Scope:          "session", // Default to session scope
			}
			m.request.ResponseChan <- response
			m.request = nil // Clear request to signal completion
			return m, nil

		case "y", "Y":
			// Quick yes
			response := permissions.PermissionResponse{
				Approved:       true,
				RememberChoice: false,
				Scope:          "session",
			}
			m.request.ResponseChan <- response
			m.request = nil // Clear request to signal completion
			return m, nil

		case "n", "N":
			// Quick no
			response := permissions.PermissionResponse{
				Approved:       false,
				RememberChoice: false,
				Scope:          "session",
			}
			m.request.ResponseChan <- response
			m.request = nil // Clear request to signal completion
			return m, nil

		case "a", "A":
			// Always allow
			response := permissions.PermissionResponse{
				Approved:       true,
				RememberChoice: true,
				Scope:          "always",
			}
			m.request.ResponseChan <- response
			m.request = nil // Clear request to signal completion
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	return m, nil
}

// View renders the permission prompt
func (m *PermissionPromptModel) View() string {
	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1)

	b.WriteString(titleStyle.Render("🔐 Permission Request"))
	b.WriteString("\n\n")

	// Tool info
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	b.WriteString(infoStyle.Render(fmt.Sprintf("Tool: %s", m.request.ToolName)))
	b.WriteString("\n")

	if m.request.ServerName != "" {
		b.WriteString(infoStyle.Render(fmt.Sprintf("Server: %s", m.request.ServerName)))
		b.WriteString("\n")
	}

	b.WriteString(infoStyle.Render(fmt.Sprintf("Description: %s", m.request.Description)))
	b.WriteString("\n\n")

	// Parameters (sanitized)
	if len(m.request.Parameters) > 0 {
		b.WriteString(infoStyle.Render("Parameters:"))
		b.WriteString("\n")

		for k, v := range m.request.Parameters {
			paramStr := fmt.Sprintf("%v", v)
			// Truncate long parameter values
			if len(paramStr) > 60 {
				paramStr = paramStr[:60] + "..."
			}
			b.WriteString(infoStyle.Render(fmt.Sprintf("  %s: %s", k, paramStr)))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Buttons
	denyStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Foreground(lipgloss.Color("9")).
		Border(lipgloss.RoundedBorder(), true)

	allowStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Foreground(lipgloss.Color("10")).
		Border(lipgloss.RoundedBorder(), true)

	selectedStyle := lipgloss.NewStyle().
		Padding(0, 2).
		Bold(true).
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("12")).
		Border(lipgloss.RoundedBorder(), true)

	var denyBtn, allowBtn string
	if m.focused == 0 {
		denyBtn = selectedStyle.Render("Deny (n)")
		allowBtn = allowStyle.Render("Allow (y)")
	} else {
		denyBtn = denyStyle.Render("Deny (n)")
		allowBtn = selectedStyle.Render("Allow (y)")
	}

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Left, denyBtn, "  ", allowBtn))
	b.WriteString("\n\n")

	// Remember checkbox
	checkbox := "[ ]"
	if m.remember {
		checkbox = "[x]"
	}

	checkboxStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	checkboxFocusedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Background(lipgloss.Color("236"))

	checkboxText := fmt.Sprintf("%s Remember for this session", checkbox)
	if m.focused == 2 {
		// Checkbox is focused
		b.WriteString(checkboxFocusedStyle.Render(checkboxText))
	} else {
		b.WriteString(checkboxStyle.Render(checkboxText))
	}
	b.WriteString("\n")

	// Help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Italic(true)

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓/Tab: navigate • Space: toggle • Enter: confirm • y/n: quick answer • a: always"))

	// Wrap in a box with shadow effect
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder(), true).
		BorderForeground(lipgloss.Color("12")).
		Background(lipgloss.Color("235")). // Dark background for modal
		Padding(1, 2).
		Width(70)

	// Add a subtle "shadow" by rendering with extra border
	shadowStyle := lipgloss.NewStyle().
		Border(lipgloss.Border{
			Top:         " ",
			Bottom:      "▀",
			Left:        " ",
			Right:       "▌",
			TopLeft:     " ",
			TopRight:    " ",
			BottomLeft:  " ",
			BottomRight: "▀",
		}, true).
		BorderForeground(lipgloss.Color("0"))

	// Render box with shadow
	boxed := boxStyle.Render(b.String())
	return shadowStyle.Render(boxed)
}

// SetSize sets the viewport size
func (m *PermissionPromptModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
