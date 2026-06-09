package ui

import (
	"encoding/json"
	"fmt"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/zulip"

	tea "github.com/charmbracelet/bubbletea"
)

// LLM and streaming response handlers

func handleLLMResponseMsg(m Model, msg llmResponseMsg) (Model, tea.Cmd) {
	m.messages = append(m.messages, chat.NewAssistantMessage(msg.content))
	m.updateViewport()
	return m, m.updateTokenCountAsync()
}

func handleErrMsg(m Model, msg errMsg) (Model, tea.Cmd) {
	m.err = msg.err
	m.isStreaming = false
	m.respondingToZulip = false
	m.updateViewport()
	return m, nil
}

func handleStreamStartMsg(m Model, msg streamStartMsg) (Model, tea.Cmd) {
	if m.tracer != nil {
		if err := m.tracer.LogStreamStart(); err != nil {
			logging.Warnf("Failed to log stream start to trace: %v", err)
		}
	}

	m.isStreaming = true
	m.messages = append(m.messages, chat.NewAssistantMessage(""))
	m.streamingMsgIndex = len(m.messages) - 1
	m.currentStreamMsg = m.currentStreamMsg[:0]
	m.currentThinking = m.currentThinking[:0]

	// Snapshot the rendered output of all stable messages (everything before
	// the new streaming placeholder) so chunk updates only re-render one message.
	m.stableCacheCount = m.streamingMsgIndex
	m.stableRenderedCache = m.renderConversationMessages(m.messages[:m.stableCacheCount], true)

	m.updateViewport()

	return m, tea.Batch(m.updateTokenCountAsync(), waitForStreamChunk(msg.stream))
}

func handleStreamChunkMsg(m Model, msg streamChunkMsg) (Model, tea.Cmd) {
	if msg.content != "" {
		m.currentStreamMsg = append(m.currentStreamMsg, msg.content...)
	}
	if msg.thinking != "" {
		m.currentThinking = append(m.currentThinking, msg.thinking...)
	}

	if msg.toolUse != nil {
		return handleToolInvokedMsg(m, toolInvokedMsg{toolUse: *msg.toolUse})
	}

	if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
		m.messages[m.streamingMsgIndex].Content = string(m.currentStreamMsg)
		m.messages[m.streamingMsgIndex].Thinking = string(m.currentThinking)
	}

	m.updateViewport()
	return m, waitForStreamChunk(msg.stream)
}

func handleStreamDoneMsg(m Model) (Model, tea.Cmd) {
	if m.tracer != nil {
		if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
			if err := m.tracer.LogAssistantMessage(string(m.currentStreamMsg), string(m.currentThinking)); err != nil {
				logging.Warnf("Failed to log assistant message to trace: %v", err)
			}
		}
		if err := m.tracer.LogStreamComplete(); err != nil {
			logging.Warnf("Failed to log stream complete to trace: %v", err)
		}
	}

	// Send response to Zulip bridge if needed
	if len(m.zulipMessages) > 0 && m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
		// Find the most recent unresponded Zulip message
		for i := len(m.zulipMessages) - 1; i >= 0; i-- {
			zulipCtx := &m.zulipMessages[i]
			if !zulipCtx.Responded && zulipCtx.BridgeReply != nil {
				assistantMsg := m.messages[m.streamingMsgIndex]
				zulipCtx.Responded = true

				// Send complete response asynchronously
				go func(ch chan<- zulip.MessageResponse, msg *zulip.Message, response string) {
					ch <- zulip.MessageResponse{
						OriginalMsg: msg,
						Response:    response,
					}
				}(zulipCtx.BridgeReply, zulipCtx.ZulipMessage, assistantMsg.Content)
				break
			}
		}
	}

	m.isStreaming = false
	m.streamingMsgIndex = -1
	m.respondingToZulip = false
	m.stableRenderedCache = "" // invalidate — full re-render on next updateViewport
	m.stableCacheCount = 0
	m.updateViewport()
	return m, tea.Batch(m.updateTokenCountAsync(), checkCompactionNeeded(&m))
}

func handleCompactionNeededMsg(m Model, msg compactionNeededMsg) (Model, tea.Cmd) {
	logging.Infof("Compaction triggered: %d tokens (threshold: %d)", msg.currentTokens, msg.threshold)
	return m, performCompaction(&m)
}

func handleCompactionDoneMsg(m Model, msg compactionDoneMsg) (Model, tea.Cmd) {
	oldCount := len(m.messages)
	m.messages = msg.newMessages
	m.messages = append([]chat.Message{msg.summary}, m.messages...)
	newCount := len(m.messages)

	logging.Infof("Compaction complete: %d → %d messages", oldCount, newCount)
	m.needsCompaction = false
	m.updateViewport()
	return m, m.updateTokenCountAsync()
}

func handleCompactionErrorMsg(m Model, msg compactionErrorMsg) (Model, tea.Cmd) {
	logging.Errorf("Compaction failed: %v", msg.err)
	m.needsCompaction = false
	return m, nil
}

func handleCommandExecutedMsg(m Model, msg commandExecutedMsg) (Model, tea.Cmd) {
	panelVisibilityChanged := false
	if metadata := msg.result.Metadata; metadata != nil {
		if action, ok := metadata["ui_action"].(string); ok {
			switch action {
			case "toggle_panel":
				panelVisibilityChanged = true
				if visible, ok := metadata["panel_visible"].(bool); ok {
					m.showAgentPanel = visible
				} else if visStr, ok := metadata["panel_visible"].(string); ok && visStr == "toggle" {
					m.showAgentPanel = !m.showAgentPanel
				}
			case "quit":
				return m, m.quit()
			case "branch_session":
				if newID, ok := metadata["session_id"].(string); ok && newID != "" {
					return m.reconnectToSession(newID, msg.result.Message)
				}
			case "show_panel_on_first_spawn":
				if m.firstSpawnSinceLaunch && m.agentManager != nil {
					panelVisibilityChanged = true
					m.showAgentPanel = true
					m.firstSpawnSinceLaunch = false
				}
			}
		}
	}

	if msg.result.Success {
		if msg.result.UpdatedState != nil {
			m.messages = msg.result.UpdatedState.Messages
			m.lastTurnInputTokens = 0
			m.lastTurnOutputTokens = 0
		}
		if msg.result.Message != "" {
			m.messages = append(m.messages, chat.NewNoteMessage(msg.result.Message))
		}
	} else {
		m.err = fmt.Errorf("%s", msg.result.Message)
		m.messages = append(m.messages, chat.NewNoteMessage(fmt.Sprintf("Error: %s", msg.result.Message)))
	}

	if panelVisibilityChanged {
		effectiveWidth := m.width
		if m.showAgentPanel {
			effectiveWidth = m.width - m.agentPanelWidth()
		}
		m.viewport.Width = effectiveWidth - 4
		m.textarea.SetWidth(effectiveWidth - 4)

		tokenCmd := m.updateTokenCountAsync()
		m.updateViewport()

		if m.showAgentPanel {
			return m, tea.Batch(tokenCmd, m.agentPanelRefreshTicker())
		}
		return m, tokenCmd
	}

	tokenCmd := m.updateTokenCountAsync()
	m.updateViewport()
	return m, tokenCmd
}

func handleToolInvokedMsg(m Model, msg toolInvokedMsg) (Model, tea.Cmd) {
	if m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
		toolSection := fmt.Sprintf("[Tool: %s]\n%s", msg.toolUse.Name, string(msg.toolUse.Parameters))
		m.messages[m.streamingMsgIndex].Content = string(m.currentStreamMsg) + "\n\n" + toolSection
		m.messages[m.streamingMsgIndex].Thinking = string(m.currentThinking)
	}

	m.isStreaming = false
	m.streamingMsgIndex = -1
	m.updateViewport()

	if m.tracer != nil {
		var params map[string]interface{}
		if err := json.Unmarshal(msg.toolUse.Parameters, &params); err != nil {
			logging.Warnf("Failed to unmarshal tool parameters for %s: %v", msg.toolUse.Name, err)
			params = map[string]interface{}{"error": "failed to parse parameters"}
		}
		if err := m.tracer.LogToolCall(msg.toolUse.Name, msg.toolUse.ID, params); err != nil {
			logging.Warnf("Failed to log tool call to trace: %v", err)
		}
	}

	return m, executeTool(&m, msg.toolUse)
}

func handleToolResultMsg(m Model, msg toolResultMsg) (Model, tea.Cmd) {
	var toolResultContent string
	if msg.result.Success {
		toolResultContent = msg.result.Content
	} else {
		toolResultContent = fmt.Sprintf("Error: %s", msg.result.Error)
	}

	if m.tracer != nil {
		if err := m.tracer.LogToolResult(msg.toolName, msg.toolUseID, toolResultContent); err != nil {
			logging.Warnf("Failed to log tool result to trace: %v", err)
		}
	}

	// Create a tool result message with metadata
	toolMsg := chat.NewToolResultMessage(msg.toolName, msg.toolUseID, toolResultContent, msg.result.Success)
	m.messages = append(m.messages, toolMsg)
	m.updateViewport()

	return m, tea.Batch(m.updateTokenCountAsync(), func() tea.Msg {
		return streamStartMsg{stream: msg.stream}
	})
}

func handleAgentSpawnedMsg(m Model, msg agentSpawnedMsg) (Model, tea.Cmd) {
	m.activeAgents[msg.agent.ID] = msg.agent

	if m.firstSpawnSinceLaunch {
		m.showAgentPanel = true
		m.firstSpawnSinceLaunch = false

		effectiveWidth := m.width
		if m.showAgentPanel {
			effectiveWidth = m.width - m.agentPanelWidth()
		}
		m.viewport.Width = effectiveWidth - 4
		m.textarea.SetWidth(effectiveWidth - 4)
		m.updateViewport()

		return m, m.agentPanelRefreshTicker()
	}

	return m, nil
}

func handleAgentMessageMsg(m Model, msg agentMessageMsg) (Model, tea.Cmd) {
	agent, exists := m.activeAgents[msg.agentID]
	if !exists {
		return m, nil
	}

	switch msg.message.Type {
	case "thinking":
		agent.Messages = append(agent.Messages, "💭 "+msg.message.Content)
	case "progress":
		if progress, ok := msg.message.Metadata["progress"].(float64); ok {
			agent.Progress = progress
		}
		agent.Messages = append(agent.Messages, "⚙️ "+msg.message.Content)
	case "result":
		agent.Messages = append(agent.Messages, "✅ "+msg.message.Content)
	case "error":
		agent.Status = agents.StatusError
		agent.Messages = append(agent.Messages, "❌ "+msg.message.Content)
	case "tool_use":
		agent.Messages = append(agent.Messages, "🔧 "+msg.message.Content)
	}

	if m.showAgentPanel {
		m.updateViewport()
	}

	return m, nil
}

func handleAgentCompletedMsg(m Model, msg agentCompletedMsg) (Model, tea.Cmd) {
	agent, exists := m.activeAgents[msg.agentID]
	if !exists {
		return m, nil
	}

	agent.Status = agents.StatusCompleted
	agent.Task = "Completed"
	agent.Progress = 1.0

	sysMsg := chat.NewSystemMessage(fmt.Sprintf("Agent %s (%s) completed:\n%s",
		msg.agentID[:8], agent.Persona, msg.content))
	m.messages = append(m.messages, sysMsg)
	m.updateViewport()

	return m, m.updateTokenCountAsync()
}

func handleAgentErrorMsg(m Model, msg agentErrorMsg) (Model, tea.Cmd) {
	agent, exists := m.activeAgents[msg.agentID]
	if !exists {
		return m, nil
	}

	agent.Status = agents.StatusError
	agent.Task = fmt.Sprintf("Error: %v", msg.err)

	sysMsg := chat.NewSystemMessage(fmt.Sprintf("Agent %s (%s) encountered error: %v",
		msg.agentID[:8], agent.Persona, msg.err))
	m.messages = append(m.messages, sysMsg)
	m.updateViewport()

	return m, m.updateTokenCountAsync()
}

func handleAgentStoppedMsg(m Model, msg agentStoppedMsg) (Model, tea.Cmd) {
	delete(m.activeAgents, msg.agentID)
	return m, nil
}

func handleAgentPanelRefreshMsg(m Model) (Model, tea.Cmd) {
	if !m.showAgentPanel || m.agentManager == nil {
		return m, nil
	}
	return m, m.agentPanelRefreshTicker()
}

func handleAgentClickedMsg(m Model, msg agentClickedMsg) (Model, tea.Cmd) {
	m.saveCurrentTextareaBuffer()
	m.viewingAgentID = msg.agentID
	m.loadTextareaBuffer(msg.agentID)
	m.updateViewport()

	cmd := m.updateTokenCountAsyncForAgentContext(msg.agentID)
	return m, cmd
}

func handleTokenCountUpdateMsg(m Model, msg tokenCountUpdateMsg) (Model, tea.Cmd) {
	m.tokenCountCache[msg.contextID] = msg.count
	if msg.contextID == m.viewingAgentID {
		m.cachedTokenCount = msg.count
		m.tokenCountDirty = false
	}
	return m, nil
}

func handleZulipMessageReceivedMsg(m Model, msg zulip.MessageReceivedMsg) (Model, tea.Cmd) {
	zulipMsg := msg.Message
	m.respondingToZulip = true
	userContent := fmt.Sprintf("[Zulip from %s] %s", zulipMsg.SenderFullName, zulipMsg.Content)
	m.messages = append(m.messages, chat.NewUserMessage(userContent))
	tokenCmd := m.updateTokenCountAsync()

	msgIndex := len(m.messages) - 1
	m.zulipMessages = append(m.zulipMessages, ZulipMessageContext{
		ZulipMsgID:   zulipMsg.ID,
		ConvIndex:    msgIndex,
		Responded:    false,
		BridgeReply:  msg.BridgeReply,
		ZulipMessage: zulipMsg,
	})

	m.updateViewport()

	var llmCmd tea.Cmd
	if m.llmClient != nil {
		llmCmd = m.sendToLLM()
	}

	if m.zulipMsgChan != nil {
		return m, tea.Batch(tokenCmd, llmCmd, waitForZulipMsg(m.zulipMsgChan))
	}
	return m, tea.Batch(tokenCmd, llmCmd)
}

func handleZulipConnectionStatusMsg(m Model, msg zulip.ConnectionStatusMsg) (Model, tea.Cmd) {
	if msg.Connected {
		m.messages = append(m.messages, chat.NewSystemMessage("✅ Zulip connected"))
	} else {
		m.messages = append(m.messages, chat.NewSystemMessage(fmt.Sprintf("❌ Zulip disconnected: %v", msg.Error)))
	}
	tokenCmd := m.updateTokenCountAsync()
	m.updateViewport()

	if m.zulipMsgChan != nil {
		return m, tea.Batch(tokenCmd, waitForZulipMsg(m.zulipMsgChan))
	}
	return m, tokenCmd
}

func handlePermissionRequestMsg(m Model, msg permissionRequestMsg) (Model, tea.Cmd) {
	// Show permission prompt in TUI
	m.activePermissionPrompt = NewPermissionPromptModel(msg.request)
	m.activePermissionPrompt.SetSize(m.width, m.height)

	var zulipCmd tea.Cmd
	// If this conversation originated from Zulip, also send prompt there
	if m.zulipBridge != nil && len(m.zulipMessages) > 0 {
		// Heuristic: check if the last Zulip message is the one that triggered this
		zulipCtx := &m.zulipMessages[len(m.zulipMessages)-1]
		if !zulipCtx.Responded {
			zulipCmd = m.sendZulipPermissionPrompt(zulipCtx, msg.request)
		}
	}

	// Don't continue listening for permission requests while prompt is active
	// The prompt will handle sending the response, then we'll restart listening
	return m, zulipCmd
}

func (m *Model) sendZulipPermissionPrompt(ctx *ZulipMessageContext, req *permissions.PermissionRequest) tea.Cmd {
	return func() tea.Msg {
		promptText := fmt.Sprintf("⚠️ **Permission Required**\n\nTool: `%s`\nReason: %s\n\nReact with ✅ to approve or ❌ to deny.",
			req.ToolName, req.Description)

		msgID, err := m.zulipBridge.SendMessage(ctx.ZulipMessage, promptText)
		if err != nil {
			logging.Errorf("Failed to send Zulip permission prompt: %v", err)
			return nil
		}

		// Add reactions for approval/denial
		go func() {
			_ = m.zulipBridge.AddReaction(msgID, "check")
			_ = m.zulipBridge.AddReaction(msgID, "x")
		}()

		return zulipPermissionPromptSentMsg{
			MessageID:        msgID,
			AuthorizedUserID: ctx.ZulipMessage.SenderID,
			Request:          req,
		}
	}
}

func handleZulipReactionMsg(m Model, msg zulip.ReactionReceivedMsg) (Model, tea.Cmd) {
	pending, ok := m.pendingZulipPermissions[msg.MessageID]
	if !ok {
		return m, nil
	}

	// Verify authorization: only the original sender can approve/deny
	if msg.UserID != pending.AuthorizedUserID {
		logging.Warnf("Unauthorized reaction from user %d on permission prompt %d (authorized user: %d)",
			msg.UserID, msg.MessageID, pending.AuthorizedUserID)
		return m, nil
	}

	// Determine if the reaction is an approval or denial
	var approved bool
	if msg.EmojiName == "check" {
		approved = true
	} else if msg.EmojiName == "x" {
		approved = false
	} else {
		// Ignore other reactions
		return m, nil
	}

	// Send response to the permission request
	select {
	case pending.Request.ResponseChan <- permissions.PermissionResponse{
		Approved:       approved,
		RememberChoice: false,
		Scope:          "session",
	}:
		// Successfully sent
	default:
		logging.Warnf("Failed to send Zulip permission response for message %d: channel full/closed", msg.MessageID)
	}

	// Cleanup
	delete(m.pendingZulipPermissions, msg.MessageID)

	// If the TUI prompt was showing this request, clear it
	if m.activePermissionPrompt != nil && m.activePermissionPrompt.request == pending.Request {
		m.activePermissionPrompt = nil
	}

	// Re-enable permission request listening in TUI
	var cmd tea.Cmd
	if m.permissionRequestChan != nil {
		cmd = waitForPermissionRequest(m.permissionRequestChan)
	}

	return m, cmd
}

type zulipPermissionPromptSentMsg struct {
	MessageID        int
	AuthorizedUserID int
	Request          *permissions.PermissionRequest
}

func handlePermissionMentionMsg(m Model, msg permissionMentionMsg) (Model, tea.Cmd) {
	// Add subtle system message about tool execution
	mentionText := fmt.Sprintf("🔧 %s", msg.mention.Message)
	m.messages = append(m.messages, chat.NewSystemMessage(mentionText))
	tokenCmd := m.updateTokenCountAsync()
	m.updateViewport()

	// Continue listening for more mentions
	if m.permissionMentionChan != nil {
		return m, tea.Batch(tokenCmd, waitForPermissionMention(m.permissionMentionChan))
	}
	return m, tokenCmd
}

// MCP handlers

// handleMCPToolsRegisteredMsg rebuilds the LLM context (system prompt + tools) after MCP
// tools finish registering. Both are derived from the same filtered view of the registry,
// keeping the system prompt text and the callable tool set consistent.
func handleMCPToolsRegisteredMsg(m Model, msg mcpToolsRegisteredMsg) (Model, tea.Cmd) {
	if msg.count == 0 {
		return m, nil
	}

	// Rebuild the unified context using the configured TUI persona.
	tuiPersona := ""
	if m.config != nil {
		tuiPersona = m.config.UI.Persona
	}
	ctx := app.BuildLLMContext(m.config, m.skillRegistry, m.promptRegistry, m.personaRegistry, tuiPersona, nil)

	// Replace the system message in-place.
	for i, msg := range m.messages {
		if msg.Role == chat.RoleSystem {
			m.messages[i] = chat.NewSystemMessage(ctx.SystemPrompt)
			break
		}
	}

	// Store the updated tool list so streamFromLLM uses the same filtered set.
	m.activeTools = ctx.Tools

	logging.Infof("Rebuilt LLM context with %d MCP tools", msg.count)
	return m, nil
}
