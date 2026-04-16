package ui

import (
	"strings"
	"time"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/logging"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Input handlers - keyboard and mouse event handling

func handleFocusMsg(m Model) (Model, tea.Cmd) {
	m.focused = true
	m.textarea.Focus()
	return m, textarea.Blink
}

func handleBlurMsg(m Model) (Model, tea.Cmd) {
	m.focused = false
	m.textarea.Blur()
	return m, nil
}

func handleKeyEsc(m Model) (Model, tea.Cmd) {
	if m.viewingAgentID != "" {
		m.saveCurrentTextareaBuffer()
		m.viewingAgentID = ""
		m.loadTextareaBuffer("")
		m.updateViewport()
		return m, nil
	}
	return m, m.quit()
}

func handleKeyCtrlC(m Model) (Model, tea.Cmd) {
	// Don't quit on Ctrl+C to allow terminal copy-paste
	// If the user really wants to quit, they can use Ctrl+Q or ESC (in main chat)
	return m, nil
}

func handleKeyCtrlQ(m Model) (Model, tea.Cmd) {
	return m, m.quit()
}

func handleKeyCtrlV(m Model) (Model, tea.Cmd) {
	str, err := clipboard.ReadAll()
	if err != nil {
		m.err = err
		return m, nil
	}
	m.textarea.InsertString(str)
	return m, nil
}

func handleKeyCtrlH(m Model) (Model, tea.Cmd) {
	m.showHelp = !m.showHelp
	m.updateViewport()
	return m, nil
}

func handleKeyCtrlA(m Model) (Model, tea.Cmd) {
	m.showAgentPanel = !m.showAgentPanel

	effectiveWidth := m.width
	if m.showAgentPanel {
		effectiveWidth = m.width - m.agentPanelWidth()
	}
	m.viewport.Width = effectiveWidth - 4
	m.textarea.SetWidth(effectiveWidth - 4)
	m.updateViewport()

	if m.showAgentPanel {
		return m, m.agentPanelRefreshTicker()
	}
	return m, nil
}

func handleKeyEnter(m Model) (Model, tea.Cmd) {
	value := strings.TrimSpace(m.textarea.Value())
	if value == "" {
		return m, nil
	}

	if strings.HasPrefix(value, "/") {
		m.textarea.Reset()
		m.clearTextareaBuffer(m.currentContext)
		model, cmd := m.handleCommand(value)
		return *model.(*Model), cmd
	}

	if m.viewingAgentID != "" {
		model, cmd := m.sendMessageToAgent(value)
		return *model.(*Model), cmd
	}

	if m.isStreaming {
		return m, nil
	}

	userMsg := chat.NewUserMessage(value)
	m.messages = append(m.messages, userMsg)
	m.respondingToZulip = false
	tokenCmd := m.updateTokenCountAsync()
	m.textarea.Reset()
	m.clearTextareaBuffer("")
	m.updateViewport()

	if m.tracer != nil {
		_ = m.tracer.LogUserMessage(value)
	}

	// Remote mode: delegate to agentd.
	if m.conn != nil {
		updated, agentCmd := m.sendMessageToAgentD(value)
		return updated, tea.Batch(tokenCmd, agentCmd)
	}

	if m.llmClient != nil {
		return m, tea.Batch(tokenCmd, m.sendToLLM())
	}
	return m, tea.Batch(tokenCmd, m.mockResponse(value))
}

func handleWindowSizeMsg(m Model, msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	effectiveWidth := msg.Width
	if m.showAgentPanel {
		panelWidth := msg.Width / 5
		if panelWidth < 25 {
			panelWidth = 25
		}
		effectiveWidth = msg.Width - panelWidth
	}

	if m.markdownRenderer != nil {
		markdownWidth := effectiveWidth - 4 - 3
		if markdownWidth < 40 {
			markdownWidth = 40
		}
		_ = m.markdownRenderer.UpdateWidth(markdownWidth)
	}

	var zulipStartCmd tea.Cmd
	if !m.ready {
		m.viewport = viewport.New(effectiveWidth-4, msg.Height-8)
		// Viewport starts after header (1 line) + newline (1 line) = line 2
		m.viewport.YPosition = 2
		m.ready = true
		m.tokenCountDirty = false
		m.updateViewport()

		if m.zulipBridge != nil && !m.zulipBridge.IsConnected() {
			zulipStartCmd = m.startZulipBridge()
		}
	} else {
		m.viewport.Width = effectiveWidth - 4
		m.viewport.Height = msg.Height - 8
		m.updateViewport()
	}

	m.textarea.SetWidth(effectiveWidth - 4)
	return m, tea.Batch(zulipStartCmd)
}

func handleMouseMsg(m Model, msg tea.MouseMsg) (Model, tea.Cmd) {
	// Debug: Log ALL mouse events to understand what we're receiving
	logging.Debugf("Mouse event received: type=%v, X=%d, Y=%d", msg.Type, msg.X, msg.Y)

	// Handle mouse release for click detection (more standard than press)
	// Note: Can change to MouseLeft if that's what your terminal sends
	if msg.Type == tea.MouseRelease || msg.Type == tea.MouseLeft {
		clickX := msg.X
		clickY := msg.Y

		// Debug logging
		logging.Debugf("Mouse click at screen position (%d, %d), type: %v", clickX, clickY, msg.Type)

		// Check for agent panel clicks
		if m.agentManager != nil && m.showAgentPanel {
			_, positions := m.renderAgentPanelWithPositions(m.agentManager)
			m.agentCardPositions = positions

			for _, pos := range m.agentCardPositions {
				if clickX >= pos.PanelX && clickX < (pos.PanelX+pos.PanelWidth) {
					if clickY >= pos.StartY && clickY < pos.EndY {
						return m, func() tea.Msg {
							return agentClickedMsg{agentID: pos.AgentID}
						}
					}
				}
			}
		}

		// Check for tool result clicks (for expand/collapse)
		// Convert screen Y to content Y (accounting for viewport position and scroll)
		contentY := clickY - m.viewport.YPosition + m.viewport.YOffset

		logging.Debugf("=== CLICK DEBUG ===")
		logging.Debugf("Screen click: X=%d, Y=%d", clickX, clickY)
		logging.Debugf("Viewport: YPosition=%d, YOffset=%d, Height=%d",
			m.viewport.YPosition, m.viewport.YOffset, m.viewport.Height)
		logging.Debugf("Coordinate conversion: clickY=%d - YPosition=%d + YOffset=%d = contentY=%d",
			clickY, m.viewport.YPosition, m.viewport.YOffset, contentY)
		logging.Debugf("Click zones registered: %d", len(m.toolResultClickZones))

		for msgIdx, zone := range m.toolResultClickZones {
			inZone := contentY >= zone.StartY && contentY < zone.EndY
			logging.Debugf("  Zone[msg=%d]: StartY=%d, EndY=%d, height=%d, inZone=%v",
				msgIdx, zone.StartY, zone.EndY, zone.EndY-zone.StartY, inZone)
			if inZone {
				logging.Infof("✓ Click matched zone for message %d", msgIdx)
				// Detect double-click
				now := time.Now()
				isDoubleClick := msgIdx == m.lastClickIndex && now.Sub(m.lastClickTime) < 500*time.Millisecond

				if isDoubleClick {
					// Toggle expanded state
					m.expandedToolResults[msgIdx] = !m.expandedToolResults[msgIdx]
					m.updateViewport()
					m.lastClickIndex = -1 // Reset to prevent triple-click
				} else {
					// Single click - record for potential double-click
					m.lastClickTime = now
					m.lastClickIndex = msgIdx
				}
				return m, nil
			}
		}
	}
	return m, nil
}
