package ui

import (
	"errors"
	"fmt"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/logging"

	tea "github.com/charmbracelet/bubbletea"
)

// handleAgentConnectedMsg is called when the WebSocket connection to agentd is established.
func handleAgentConnectedMsg(m Model, msg agentConnectedMsg) (Model, tea.Cmd) {
	m.conn = msg.conn
	m.agentClient = msg.client
	m.sessionID = msg.sessionID
	m.agentController = agentproto.NewClientAgentController(msg.client, msg.sessionID)

	// Replace the "Connecting..." welcome note with a connected notice.
	for i, chatMsg := range m.messages {
		if chatMsg.Role == chat.RoleNote {
			m.messages[i] = chat.NewNoteMessage(
				fmt.Sprintf("Connected to agentd (session %s)", msg.sessionID[:8]),
			)
			break
		}
	}

	m.updateViewport()
	logging.Infof("Connected to agentd, session %s", msg.sessionID)
	return m, nil
}

// handleAgentDisconnectedMsg is called when the agentd connection is lost.
func handleAgentDisconnectedMsg(m Model, msg agentDisconnectedMsg) (Model, tea.Cmd) {
	m.conn = nil
	m.isStreaming = false
	m.streamingMsgIndex = -1

	errText := "disconnected from agentd"
	if msg.err != nil {
		errText = msg.err.Error()
	}
	m.messages = append(m.messages, chat.NewSystemMessage("Error: "+errText))
	m.updateViewport()
	logging.Errorf("agentd disconnected: %v", msg.err)
	return m, nil
}

// handleAgentEventMsg dispatches a server→client event from agentd.
func handleAgentEventMsg(m Model, msg agentEventMsg) (Model, tea.Cmd) {
	return m.handleAgentEvent(msg.event)
}

// handleAgentEvent processes a single agentd Event and returns the updated model and next command.
func (m Model) handleAgentEvent(e agentproto.Event) (Model, tea.Cmd) {
	switch e.Type {
	case agentproto.EventTypeChunk:
		if e.Chunk == nil {
			return m, waitForEvent(m.conn.Events())
		}
		if e.Chunk.Content != "" {
			m.currentStreamMsg = append(m.currentStreamMsg, e.Chunk.Content...)
		}
		if e.Chunk.Thinking != "" {
			m.currentThinking = append(m.currentThinking, e.Chunk.Thinking...)
		}
		if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
			m.messages[m.streamingMsgIndex].Content = string(m.currentStreamMsg)
			m.messages[m.streamingMsgIndex].Thinking = string(m.currentThinking)
		}
		m.updateViewport()
		return m, waitForEvent(m.conn.Events())

	case agentproto.EventTypeDone:
		if e.Done != nil {
			// Prefer the authoritative full content from the Done event.
			if e.Done.FullContent != "" {
				m.currentStreamMsg = append(m.currentStreamMsg[:0], e.Done.FullContent...)
			}
			if e.Done.FullThinking != "" {
				m.currentThinking = append(m.currentThinking[:0], e.Done.FullThinking...)
			}
			if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
				m.messages[m.streamingMsgIndex].Content = string(m.currentStreamMsg)
				m.messages[m.streamingMsgIndex].Thinking = string(m.currentThinking)
			}
			if e.Done.InputTokens > 0 || e.Done.OutputTokens > 0 {
				m.lastTurnInputTokens = e.Done.InputTokens
				m.lastTurnOutputTokens = e.Done.OutputTokens
			}
		}
		m.isStreaming = false
		m.streamingMsgIndex = -1
		m.currentStreamMsg = m.currentStreamMsg[:0]
		m.currentThinking = m.currentThinking[:0]
		m.updateViewport()
		return m, m.updateTokenCountAsync()

	case agentproto.EventTypeToolInvoked:
		if e.ToolInvoked == nil {
			return m, waitForEvent(m.conn.Events())
		}
		// Show tool invocation inline in the streaming message.
		if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
			toolSection := fmt.Sprintf("[Tool: %s]\n%s", e.ToolInvoked.ToolName, string(e.ToolInvoked.Params))
			m.messages[m.streamingMsgIndex].Content = string(m.currentStreamMsg) + "\n\n" + toolSection
			m.messages[m.streamingMsgIndex].Thinking = string(m.currentThinking)
		}
		m.updateViewport()
		return m, waitForEvent(m.conn.Events())

	case agentproto.EventTypeToolResult:
		if e.ToolResult == nil {
			return m, waitForEvent(m.conn.Events())
		}
		// Add a tool result message to the conversation log.
		toolMsg := chat.NewToolResultMessage(
			e.ToolResult.ToolName,
			e.ToolResult.ToolUseID,
			e.ToolResult.Result,
			!e.ToolResult.IsError,
		)
		m.messages = append(m.messages, toolMsg)

		// Reset the streaming assistant message for the continuation response.
		m.messages = append(m.messages, chat.NewAssistantMessage(""))
		m.streamingMsgIndex = len(m.messages) - 1
		m.currentStreamMsg = m.currentStreamMsg[:0]
		m.currentThinking = m.currentThinking[:0]
		m.updateViewport()
		return m, waitForEvent(m.conn.Events())

	case agentproto.EventTypePermRequest:
		if e.PermRequest == nil {
			return m, waitForEvent(m.conn.Events())
		}
		// Show permission prompt modal (same UI as local mode).
		req := permRequestFromAgentEvent(e.PermRequest)
		m.activePermissionPrompt = NewPermissionPromptModelFromAgent(req, m.conn, e.PermRequest.RequestID)
		m.activePermissionPrompt.SetSize(m.width, m.height)
		// Do NOT continue reading events — the modal will resume after user responds.
		return m, nil

	case agentproto.EventTypePermMention:
		if e.PermMention == nil {
			return m, waitForEvent(m.conn.Events())
		}
		mentionText := fmt.Sprintf("Tool %s ran: %s", e.PermMention.ToolName, e.PermMention.Description)
		m.messages = append(m.messages, chat.NewSystemMessage(mentionText))
		m.updateViewport()
		return m, waitForEvent(m.conn.Events())

	case agentproto.EventTypeCompaction:
		if e.Compaction != nil {
			note := fmt.Sprintf("Conversation compacted (%d → %d messages): %s",
				e.Compaction.MessagesBefore, e.Compaction.MessagesAfter, e.Compaction.Summary)
			m.messages = append(m.messages, chat.NewSummaryMessage(note))
			m.updateViewport()
		}
		return m, waitForEvent(m.conn.Events())

	case agentproto.EventTypeError:
		if e.Error == nil {
			return m, waitForEvent(m.conn.Events())
		}
		m.messages = append(m.messages, chat.NewSystemMessage("Error: "+e.Error.Message))
		m.updateViewport()
		if e.Error.Fatal {
			m.isStreaming = false
			m.streamingMsgIndex = -1
			return m, func() tea.Msg {
				return agentDisconnectedMsg{err: errors.New(e.Error.Message)}
			}
		}
		return m, waitForEvent(m.conn.Events())
	}

	// Unknown event type — keep listening.
	return m, waitForEvent(m.conn.Events())
}

// sendMessageToAgentD sends a user message to agentd and starts streaming.
func (m Model) sendMessageToAgentD(content string) (Model, tea.Cmd) {
	if m.conn == nil {
		m.messages = append(m.messages, chat.NewSystemMessage("Error: not connected to agentd"))
		m.updateViewport()
		return m, nil
	}

	// Append empty assistant placeholder.
	m.messages = append(m.messages, chat.NewAssistantMessage(""))
	m.streamingMsgIndex = len(m.messages) - 1
	m.isStreaming = true
	m.currentStreamMsg = m.currentStreamMsg[:0]
	m.currentThinking = m.currentThinking[:0]
	m.updateViewport()

	conn := m.conn
	cmd := func() tea.Msg {
		if err := conn.Send(agentproto.ClientMessage{
			Type:        agentproto.MessageTypeSendMessage,
			SendMessage: &agentproto.SendMessagePayload{Content: content},
		}); err != nil {
			return agentDisconnectedMsg{err: err}
		}
		return waitForEvent(conn.Events())()
	}

	return m, cmd
}
