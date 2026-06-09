package ui

import (
	"fmt"
	"strings"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/logging"

	"github.com/charmbracelet/lipgloss"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m Model) calculateBoxWidth() int {
	boxWidth := m.width - 8
	if boxWidth < 10 {
		boxWidth = 10
	}
	return boxWidth
}

func (m Model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	if m.showHelp {
		return m.renderHelp()
	}

	var b strings.Builder

	// Header with token counter
	header := m.renderHeader()
	b.WriteString(header)
	b.WriteString("\n")

	// Viewport with messages (thinking is now rendered inline)
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")

	// Input area
	b.WriteString(inputBoxStyle.Render(m.textarea.View()))
	b.WriteString("\n")

	// Status line
	statusLine := m.renderStatusLine()
	b.WriteString(statusLine)

	mainView := b.String()

	// Render agent panel if enabled (stays visible in both modes)
	if m.showAgentPanel && m.agentManager != nil {
		agentPanel := m.renderAgentPanel(m.agentManager)
		if agentPanel != "" {
			mainView = lipgloss.JoinHorizontal(lipgloss.Top, mainView, agentPanel)
		}
	}

	// Show permission prompt overlay if active
	if m.activePermissionPrompt != nil {
		return m.renderWithPermissionOverlay(mainView, m.activePermissionPrompt.View())
	}

	return mainView
}

// renderConversationMessages renders a list of messages (extracted for reuse)
func (m *Model) renderConversationMessages(messages []chat.Message, isMainChat bool) string {
	var b strings.Builder
	currentY := 0

	// Clear and rebuild click zones for tool results
	if isMainChat {
		m.toolResultClickZones = make(map[int]ClickZone)
	}

	for i, msg := range messages {
		// For click zones, include the separator line in the startY
		startY := currentY

		if i > 0 {
			b.WriteString("\n")
			currentY++
		}

		switch msg.Role {
		case chat.RoleUser:
			b.WriteString(userMessageStyle.Render("You:"))
			b.WriteString("\n")
			b.WriteString(messageContentStyle.Render(msg.Content))
			currentY += 2 + strings.Count(msg.Content, "\n")

		case chat.RoleAssistant:
			prefix := assistantMessageStyle.Render("Assistant:")
			if isMainChat && m.isStreaming && i == m.streamingMsgIndex {
				prefix = assistantMessageStyle.Render("Assistant:") + " " + m.spinner.View()
			}
			b.WriteString(prefix)
			b.WriteString("\n")
			content := m.renderAssistantContent(msg)
			b.WriteString(content)
			currentY += 2 + strings.Count(content, "\n")

		case chat.RoleSystem:
			if m.config != nil && m.config.UI.HideSystemPrompt {
				continue
			}
			b.WriteString(systemMessageStyle.Render("System:"))
			b.WriteString("\n")
			b.WriteString(messageContentStyle.Render(msg.Content))
			currentY += 2 + strings.Count(msg.Content, "\n")

		case chat.RoleSummary:
			b.WriteString(summaryMessageStyle.Render("📦 Summary:"))
			b.WriteString("\n")
			b.WriteString(messageContentStyle.Render(msg.Content))
			currentY += 2 + strings.Count(msg.Content, "\n")

		case chat.RoleTool:
			// Render tool result with expand/collapse
			expanded := m.expandedToolResults[i]
			toolBox := m.renderToolResultBox(msg, i, expanded)
			b.WriteString(toolBox)

			// Track click zone for this tool result
			if isMainChat {
				newlineCount := strings.Count(toolBox, "\n")
				// endY should be currentY + newlineCount (where content actually is)
				// not startY + newlineCount (which would include the separator twice)
				endY := currentY + newlineCount

				logging.Debugf("Tool result msg[%d]: startY=%d, currentY=%d, newlineCount=%d, endY=%d, toolName=%s",
					i, startY, currentY, newlineCount, endY, msg.ToolName)
				logging.Debugf("  toolBox preview: %q", toolBox[:min(100, len(toolBox))])

				m.toolResultClickZones[i] = ClickZone{
					MessageIndex: i,
					StartY:       startY,
					EndY:         endY,
				}
				currentY = endY
			}
		case chat.RoleNote:
			// Subtle rendering for welcome/info messages
			content := helpStyle.Render(msg.Content)
			b.WriteString(content)
			b.WriteString("\n")
			currentY += 1 + strings.Count(content, "\n")
		}

		//b.WriteString("\n")
		//currentY++
	}

	return b.String()
}

// renderMessages converts messages to formatted strings for the viewport
func (m *Model) renderMessages() string {
	return m.renderConversationMessages(m.messages, true)
}

// renderSingleMessage renders the message at the given index in isolation,
// prefixed with a separator newline if it follows other content.
// Used by the streaming fast path to avoid re-rendering the full conversation.
func (m *Model) renderSingleMessage(idx int) string {
	if idx < 0 || idx >= len(m.messages) {
		return ""
	}
	single := m.renderConversationMessages(m.messages[idx:idx+1], false)
	if idx > 0 {
		return "\n" + single
	}
	return single
}

// renderStatusLine renders the bottom status line
func (m Model) renderStatusLine() string {
	var status string

	if m.ctrlCPending {
		status = errorStyle.Render("Press Ctrl+C again to quit")
	} else if m.err != nil {
		status = errorStyle.Render(fmt.Sprintf("Error: %v", m.err))
	} else if m.isStreaming {
		status = helpStyle.Render("Streaming response...")
	} else {
		contextHelp := "main chat"
		if m.viewingAgentID != "" {
			contextHelp = "agent conversation"
		}
		status = helpStyle.Render(fmt.Sprintf("Input: %s • Enter to send • ESC to return", contextHelp))
	}

	// Add Zulip status indicator if bridge is configured
	if m.zulipBridge != nil {
		if m.zulipBridge.IsConnected() {
			status += " • 🟢 Zulip"
		} else {
			status += " • 🔴 Zulip"
		}
	}

	return status
}

// renderAssistantContent renders assistant message content with styled sections.
// Thinking is read from msg.Thinking directly; tool sections embedded in msg.Content
// are parsed out of the content string (they use [Tool: name] markers, not thinking markers).
func (m Model) renderAssistantContent(msg chat.Message) string {
	var result strings.Builder

	// Render thinking section from the dedicated field.
	if msg.Thinking != "" {
		thinkingLines := strings.Split(msg.Thinking, "\n")
		var nonEmpty []string
		for _, l := range thinkingLines {
			if l != "" {
				nonEmpty = append(nonEmpty, l)
			}
		}
		if len(nonEmpty) > 0 {
			result.WriteString(m.renderThinkingBox(nonEmpty))
			logging.Debugf("Rendered thinking section")
		}
	}

	// Parse msg.Content for [Tool: name] sections and the response body.
	lines := strings.Split(msg.Content, "\n")
	inTool := false
	currentToolName := ""
	var responseLines []string
	var toolSections []struct {
		name  string
		lines []string
	}
	var currentToolLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "[Tool: ") && strings.HasSuffix(line, "]") {
			if inTool && currentToolName != "" {
				toolSections = append(toolSections, struct {
					name  string
					lines []string
				}{currentToolName, currentToolLines})
			}
			inTool = true
			currentToolName = strings.TrimSuffix(strings.TrimPrefix(line, "[Tool: "), "]")
			currentToolLines = nil
			continue
		}
		if inTool {
			currentToolLines = append(currentToolLines, line)
		} else {
			responseLines = append(responseLines, line)
		}
	}
	if inTool && currentToolName != "" {
		toolSections = append(toolSections, struct {
			name  string
			lines []string
		}{currentToolName, currentToolLines})
	}

	// Render tool sections if present.
	for i, toolSection := range toolSections {
		result.WriteString(m.renderToolBox(toolSection.name, toolSection.lines))
		if i < len(toolSections)-1 {
			result.WriteString("\n")
		}
	}

	// Render response body.
	responseContent := strings.Join(responseLines, "\n")
	responseContent = strings.TrimRight(responseContent, "\n")
	if responseContent != "" {
		logging.Debugf("Rendering response section with %d lines", len(responseLines))
		if m.markdownRenderer != nil {
			renderedContent := m.markdownRenderer.Render(responseContent)
			paddedContent := strings.TrimRight(renderedContent, "\n")
			result.WriteString("   " + strings.ReplaceAll(paddedContent, "\n", "\n   "))
			result.WriteString("\n")
		} else {
			result.WriteString(messageContentStyle.Render(responseContent))
		}
	}

	finalResult := result.String()
	trailingNewlines := len(finalResult) - len(strings.TrimRight(finalResult, "\n"))
	if trailingNewlines > 0 {
		logging.Debugf("Assistant content ends with %d trailing newlines", trailingNewlines)
	}
	return strings.TrimRight(finalResult, "\n")
}

// renderThinkingBox renders thinking content in a limited-height scrollable box
func (m Model) renderThinkingBox(thinkingLines []string) string {
	const maxLines = 2 // Maximum number of lines to show

	var displayLines []string
	moreLines := 0

	if len(thinkingLines) <= maxLines {
		displayLines = thinkingLines
	} else {
		displayLines = thinkingLines[len(thinkingLines)-maxLines:]
		moreLines = len(thinkingLines) - maxLines
	}

	var content strings.Builder
	content.WriteString("💭 Thinking")

	if moreLines > 0 {
		content.WriteString(fmt.Sprintf(" (+%d more)", moreLines))
	}
	content.WriteString("\n")

	for i, line := range displayLines {
		content.WriteString(line)
		if i < len(displayLines)-1 {
			content.WriteString("\n")
		}
	}

	return thinkingBoxStyle.Copy().Width(m.calculateBoxWidth()).Render(content.String())
}

// renderToolBox renders tool usage in a styled box
func (m Model) renderToolBox(toolName string, toolLines []string) string {
	const maxLines = 2 // Maximum number of lines to show

	var content strings.Builder
	content.WriteString("🔧 Tool: ")
	content.WriteString(toolName)

	var displayLines []string
	moreLines := 0

	if len(toolLines) <= maxLines {
		displayLines = toolLines
	} else {
		// Show first few lines and indicate there's more
		displayLines = toolLines[:maxLines]
		moreLines = len(toolLines) - maxLines
	}

	if moreLines > 0 {
		content.WriteString(fmt.Sprintf(" (+%d more lines)", moreLines))
	}
	content.WriteString("\n")

	for i, line := range displayLines {
		content.WriteString(line)
		if i < len(displayLines)-1 {
			content.WriteString("\n")
		}
	}

	return toolBoxStyle.Width(m.calculateBoxWidth()).Render(content.String())
}

// renderToolResultBox renders a tool result message with expand/collapse functionality
func (m Model) renderToolResultBox(msg chat.Message, msgIndex int, expanded bool) string {
	const maxLines = 1 // Maximum number of lines to show when collapsed

	var content strings.Builder

	// Header with tool name and status
	icon := "🔧"
	if !msg.ToolSuccess {
		icon = "⚠️"
	}

	content.WriteString(icon + " Tool Result: ")
	content.WriteString(msg.ToolName)

	// Split content into lines
	lines := strings.Split(msg.Content, "\n")

	var displayLines []string
	moreLines := 0

	if expanded || len(lines) <= maxLines {
		displayLines = lines
	} else {
		// Show only first maxLines
		displayLines = lines[:maxLines]
		moreLines = len(lines) - maxLines
	}

	if moreLines > 0 {
		content.WriteString(fmt.Sprintf(" (+%d more lines - double-click to expand)", moreLines))
	} else if len(lines) > maxLines && expanded {
		content.WriteString(" (double-click to collapse)")
	}
	content.WriteString("\n")

	for _, line := range displayLines {
		content.WriteString(line)
		content.WriteString("\n")
	}

	// Use a different style for tool results
	return toolResultBoxStyle.Width(m.calculateBoxWidth()).Render(content.String())
}

// renderHelp renders the help screen
func (m Model) renderHelp() string {
	help := lipgloss.NewStyle().
		Padding(2, 4).
		Render(`
Stoa - Help

Keyboard Shortcuts:
  Enter       Send message
  Ctrl+Q      Quit application
  Ctrl+V      Paste from clipboard
  Ctrl+H      Toggle this help screen
  Ctrl+A      Toggle agent panel

  Arrow Up    Scroll up in chat history
  Arrow Down  Scroll down in chat history
  Page Up     Scroll up one page
  Page Down   Scroll down one page

Agent Panel Commands:
  /agents show     Show agent panel
  /agents hide     Hide agent panel
  /agents toggle   Toggle panel visibility
  /agents          List all active agents
  /agents list     List all active agents (explicit)

Features:
  • Real-time streaming responses (Phase 4)
  • OpenAI-compatible API support
  • Configurable via ~/.config/stoa/config.yaml
  • Environment variable support

Current Status:
  Messages: ` + fmt.Sprintf("%d", len(m.messages)) + `

Press Ctrl+H again to return to chat.
`)

	return help
}

// renderAgentPanelWithPositions renders the agent panel and returns both the rendered output and click positions
// Returns (rendered string, positions map)
func (m Model) renderAgentPanelWithPositions(mgr *agents.Manager) (string, map[string]AgentCardPosition) {
	if !m.showAgentPanel {
		return "", make(map[string]AgentCardPosition)
	}

	// Calculate panel width: 20% of terminal, minimum 25 chars
	panelWidth := m.agentPanelWidth()

	var b strings.Builder
	b.WriteString(agentPanelTitleStyle.Render("Agents"))
	b.WriteString("\n\n")

	// Track positions
	positions := make(map[string]AgentCardPosition)
	// Start agent cards at Y=1 to match the 1-line margin at top of sidebar
	currentY := 1

	// Get all agents from manager (active, completed, and errored)
	agentsList := mgr.ListAgents()

	if len(agentsList) == 0 {
		b.WriteString(helpStyle.Render("No agents"))
	} else {
		for _, agent := range agentsList {
			// Store starting position
			startY := currentY

			// Render the card
			cardContent := m.renderAgentCardFromManager(agent)
			b.WriteString(cardContent)

			// Calculate card height accounting for word wrapping within panel width
			// Account for panel padding (2 chars on each side from Padding(1, 2))
			availableWidth := panelWidth - 4
			if availableWidth < 1 {
				availableWidth = 1 // Minimum width
			}

			// Render card content with width constraint to account for text wrapping
			wrappedCard := lipgloss.NewStyle().Width(availableWidth).Render(cardContent)

			// Count lines in the width-constrained rendered output
			lines := strings.Split(wrappedCard, "\n")
			// If content ends with \n, split creates an empty element at the end
			if len(lines) > 1 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			// Subtract 1 to account for the extra line that gets added during rendering
			cardHeight := len(lines) - 1
			if cardHeight < 0 {
				cardHeight = 0
			}
			endY := startY + cardHeight

			// Store position info for click detection
			positions[agent.ID()] = AgentCardPosition{
				AgentID:    agent.ID(),
				StartY:     startY,
				EndY:       endY,
				PanelX:     m.width - panelWidth,
				PanelWidth: panelWidth,
			}

			// Update current Y position
			currentY = endY + 1 // +1 for spacing between cards

			b.WriteString("\n")
		}
	}

	// Use calculated width and full height for panel style
	style := agentPanelStyle.Copy().Width(panelWidth).Height(m.height)
	return style.Render(b.String()), positions
}

// renderAgentPanel renders the agent panel showing all agents (active and completed)
func (m Model) renderAgentPanel(mgr *agents.Manager) string {
	rendered, _ := m.renderAgentPanelWithPositions(mgr)
	return rendered
}

// renderAgentCardFromManager renders a single agent's status card from the manager's Agent interface
func (m Model) renderAgentCardFromManager(agent agents.Agent) string {
	var b strings.Builder

	// Agent header: persona + status on same line
	status := agent.Status()
	header := fmt.Sprintf("%s - %s", agent.Persona().Name(), status)

	// Check if this agent is currently being viewed
	isSelected := m.viewingAgentID == agent.ID()

	// Apply selection styling with status-based color
	switch status {
	case agents.StatusRunning:
		b.WriteString(agentStatusRunningStyle.Render(header))
	case agents.StatusCompleted:
		b.WriteString(agentStatusCompletedStyle.Render(header))
	case agents.StatusError:
		b.WriteString(agentStatusErrorStyle.Render(header))
	default:
		if isSelected {
			b.WriteString(agentHeaderSelectedStyle.Render(header))
		} else {
			b.WriteString(agentHeaderStyle.Render(header))
		}
	}
	b.WriteString("\n")

	// Progress bar
	progress := agent.Progress()
	if progress > 0 {
		b.WriteString(m.renderProgressBar(progress))
		b.WriteString("\n")
	}

	// Current task (truncated)
	if task := agent.CurrentTask(); task != nil {
		taskDesc := task.Description
		if len(taskDesc) > 40 {
			taskDesc = taskDesc[:37] + "..."
		}
		b.WriteString(fmt.Sprintf("Task: %s\n", taskDesc))
	}

	return b.String()
}

// renderProgressBar renders a progress bar for agent status
func (m Model) renderProgressBar(progress float64) string {
	width := 20
	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	percentage := fmt.Sprintf("%.0f%%", progress*100)
	return fmt.Sprintf("[%s] %s", bar, percentage)
}

// renderAgentConversation renders an agent's conversation
func (m *Model) renderAgentConversation(agentID string) string {
	// Get the agent from manager
	agent, ok := m.agentManager.GetAgent(agentID)
	if !ok {
		return errorStyle.Render("Error: Agent not found. Press ESC to return to main chat.")
	}

	// Type assert to BaseAgent to access GetConversation()
	baseAgent, ok := agent.(*agents.BaseAgent)
	if !ok {
		return errorStyle.Render("Error: Cannot access agent conversation. Press ESC to return to main chat.")
	}

	// Get agent's conversation (returns a safe copy)
	messages := baseAgent.GetConversation()

	// Render using shared helper
	return m.renderConversationMessages(messages, false)
}

// renderHeader renders the header with title on left and token count on right
func (m Model) renderHeader() string {
	rightText := m.getTokenCountText()
	if rightText == "" {
		return ""
	}

	// Calculate effective width (account for agent panel if visible)
	width := m.width
	if width == 0 {
		width = 80
	}

	// If agent panel is showing, reduce width to make room for it
	if m.showAgentPanel {
		width = m.width - m.agentPanelWidth()
	}

	rightWidth := len(rightText)

	// Calculate padding needed to push to right
	paddingNeeded := width - rightWidth - 1
	if paddingNeeded < 1 {
		paddingNeeded = 1
	}

	// Apply grey styling to the token count
	styledText := tokenCounterStyle.Render(rightText)
	return strings.Repeat(" ", paddingNeeded) + styledText
}

// getTokenCountText returns the formatted token count string for the current context.
func (m Model) getTokenCountText() string {
	// Real API token counts from agentd DoneEvent — available in both remote
	// and standalone modes once the first turn completes.
	if m.lastTurnInputTokens > 0 || m.lastTurnOutputTokens > 0 {
		return fmt.Sprintf("In: %d  Out: %d", m.lastTurnInputTokens, m.lastTurnOutputTokens)
	}

	// Remote mode before first turn: no local counter, nothing to show yet.
	if m.tokenCounter == nil {
		return "Tokens: —"
	}

	// Standalone mode: use local estimate.
	if !m.tokenCountDirty {
		contextID := m.viewingAgentID
		if count, ok := m.tokenCountCache[contextID]; ok {
			return fmt.Sprintf("Tokens: %d", count)
		}
	}

	return "Tokens: ..."
}

// renderWithPermissionOverlay renders a permission prompt overlay on top of the main view
func (m Model) renderWithPermissionOverlay(mainView string, promptView string) string {
	// Dim the background by applying a semi-transparent overlay
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	lines := strings.Split(mainView, "\n")
	promptLines := strings.Split(promptView, "\n")

	// Calculate vertical centering
	mainHeight := len(lines)
	promptHeight := len(promptLines)
	startLine := (mainHeight - promptHeight) / 2
	if startLine < 0 {
		startLine = 0
	}

	// First pass: dim all background lines
	for i := range lines {
		if lines[i] != "" {
			// Strip existing styles and apply dim
			lines[i] = dimStyle.Render(lines[i])
		}
	}

	// Second pass: overlay the prompt (completely replace lines in the prompt area)
	for i, promptLine := range promptLines {
		targetLine := startLine + i
		if targetLine >= 0 && targetLine < len(lines) {
			// Center horizontally - completely replace the line
			promptWidth := lipgloss.Width(promptLine)
			padding := (m.width - promptWidth) / 2
			if padding < 0 {
				padding = 0
			}

			// Replace entire line with centered prompt
			// Use dim style for padding to maintain background appearance
			leftPad := dimStyle.Render(strings.Repeat(" ", padding))
			rightPadLen := m.width - padding - promptWidth
			if rightPadLen < 0 {
				rightPadLen = 0
			}
			rightPad := dimStyle.Render(strings.Repeat(" ", rightPadLen))

			lines[targetLine] = leftPad + promptLine + rightPad
		}
	}

	return strings.Join(lines, "\n")
}
