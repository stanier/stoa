package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Color palette
	primaryColor   = lipgloss.Color("#7D56F4")
	userColor      = lipgloss.Color("#04B575")
	assistantColor = lipgloss.Color("#3B82F6")
	systemColor    = lipgloss.Color("#EF4444")
	subtleColor    = lipgloss.Color("#6C7086")
	borderColor    = lipgloss.Color("#45475A")

	// Base styles
	baseStyle = lipgloss.NewStyle().
			Padding(0, 1)

	// Header style
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(borderColor).
			Padding(0, 1)

	// Token counter style
	tokenCounterStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Italic(true)

	// Message container styles
	userMessageStyle = lipgloss.NewStyle().
				Foreground(userColor).
				Bold(true).
				PaddingLeft(1)

	assistantMessageStyle = lipgloss.NewStyle().
				Foreground(assistantColor).
				Bold(true).
				PaddingLeft(1)

	systemMessageStyle = lipgloss.NewStyle().
				Foreground(systemColor).
				Bold(true).
				PaddingLeft(1)

	summaryMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFA500")).
				Bold(true).
				PaddingLeft(1)

	// Content styles
	messageContentStyle = lipgloss.NewStyle().
				PaddingLeft(3).
				PaddingBottom(1)

	// Input area styles
	inputBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	// Help text styles
	helpStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Italic(true).
			Padding(0, 1)

	// Error styles
	errorStyle = lipgloss.NewStyle().
			Foreground(systemColor).
			Bold(true).
			Padding(0, 1)

	// Spinner style
	spinnerStyle = lipgloss.NewStyle().
			Foreground(primaryColor)

	// Thinking box style (for thinking content in message)
	thinkingBoxStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Italic(true).
			PaddingLeft(3).
			PaddingRight(1).
			PaddingTop(0).
			PaddingBottom(0).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(subtleColor)

	// Tool box style (for tool usage in message)
	// No border to save vertical space
	toolBoxStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")). // Orange color
			PaddingLeft(1).
			PaddingRight(1).
			PaddingTop(0).
			PaddingBottom(0)

	// Tool result box style (for tool results - distinct from tool calls)
	// No border to save vertical space
	toolResultBoxStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#00CED1")). // Dark turquoise
				PaddingLeft(1).
				PaddingRight(1).
				PaddingTop(0).
				PaddingBottom(0)

	// Agent panel styles (width set dynamically in renderAgentPanel)
	agentPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(borderColor).
			Padding(1, 2)

	agentPanelTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#00FFFF")).
				Bold(true)

	agentHeaderStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD700")).
				Bold(true)

	// Selected agent header style (highlighted background)
	agentHeaderSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFD700")).
				Background(lipgloss.Color("#45475A")).
				Bold(true)

	agentStatusRunningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#00FF00"))

	agentStatusCompletedStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color("#808080"))

	agentStatusErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FF0000"))
)
