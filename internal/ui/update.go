package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/skills"
	"github.com/stanier/stoa/internal/zulip"

	tea "github.com/charmbracelet/bubbletea"
)

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd         tea.Cmd
		vpCmd         tea.Cmd
		spCmd         tea.Cmd
		zulipStartCmd tea.Cmd
	)

	// Start Zulip bridge on first window size message (when UI is ready)
	if m.zulipBridge != nil && m.ready && !m.zulipBridge.IsConnected() {
		if _, ok := msg.(tea.WindowSizeMsg); ok {
			zulipStartCmd = m.startZulipBridge()
		}
	}

	// If permission prompt is active, route input there
	if m.activePermissionPrompt != nil {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			updatedPrompt, promptCmd := m.activePermissionPrompt.Update(keyMsg)
			m.activePermissionPrompt = updatedPrompt

			// Check if prompt was dismissed (response sent - request cleared)
			if updatedPrompt.request == nil {
				// Prompt closed, clear it and resume listening
				m.activePermissionPrompt = nil
				if m.conn != nil {
					// Remote mode: resume reading events from agentd.
					return m, tea.Batch(spCmd, promptCmd, waitForEvent(m.conn.Events()))
				}
				if m.permissionRequestChan != nil {
					return m, tea.Batch(spCmd, promptCmd, waitForPermissionRequest(m.permissionRequestChan))
				}
			}

			return m, tea.Batch(spCmd, promptCmd)
		}
	}

	// Translate mouse events for textarea
	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		// Y offset: Header(1) + Gap(1) + Viewport(Height) + Gap(2) + Border(1)
		yOffset := m.viewport.Height + 5
		// X offset: Border(1) + Padding(1)
		xOffset := 2

		translatedMsg := mouseMsg
		translatedMsg.X -= xOffset
		translatedMsg.Y -= yOffset

		m.textarea, tiCmd = m.textarea.Update(translatedMsg)
	} else {
		m.textarea, tiCmd = m.textarea.Update(msg)
	}
	m.spinner, spCmd = m.spinner.Update(msg)

	// Filter out unwanted keys before passing to viewport
	shouldUpdateViewport := true
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "j", "k", "b", "d", "u", "f", " ":
			shouldUpdateViewport = false
		}
	}

	if shouldUpdateViewport {
		m.viewport, vpCmd = m.viewport.Update(msg)
	} else {
		vpCmd = nil
	}

	// Dispatch to message-specific handlers
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.FocusMsg:
		m, cmd = handleFocusMsg(m)
	case tea.BlurMsg:
		m, cmd = handleBlurMsg(m)
	case tea.KeyMsg:
		m, cmd = m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		m, cmd = handleWindowSizeMsg(m, msg)
	case tea.MouseMsg:
		m, cmd = handleMouseMsg(m, msg)
	case llmResponseMsg:
		m, cmd = handleLLMResponseMsg(m, msg)
	case errMsg:
		m, cmd = handleErrMsg(m, msg)
	case streamStartMsg:
		m, cmd = handleStreamStartMsg(m, msg)
	case streamChunkMsg:
		m, cmd = handleStreamChunkMsg(m, msg)
	case streamDoneMsg:
		m, cmd = handleStreamDoneMsg(m)
	case compactionNeededMsg:
		m, cmd = handleCompactionNeededMsg(m, msg)
	case compactionDoneMsg:
		m, cmd = handleCompactionDoneMsg(m, msg)
	case compactionErrorMsg:
		m, cmd = handleCompactionErrorMsg(m, msg)
	case commandExecutedMsg:
		m, cmd = handleCommandExecutedMsg(m, msg)
	case toolInvokedMsg:
		m, cmd = handleToolInvokedMsg(m, msg)
	case toolResultMsg:
		m, cmd = handleToolResultMsg(m, msg)
	case agentSpawnedMsg:
		m, cmd = handleAgentSpawnedMsg(m, msg)
	case agentMessageMsg:
		m, cmd = handleAgentMessageMsg(m, msg)
	case agentCompletedMsg:
		m, cmd = handleAgentCompletedMsg(m, msg)
	case agentErrorMsg:
		m, cmd = handleAgentErrorMsg(m, msg)
	case agentStoppedMsg:
		m, cmd = handleAgentStoppedMsg(m, msg)
	case agentPanelRefreshMsg:
		m, cmd = handleAgentPanelRefreshMsg(m)
	case agentClickedMsg:
		m, cmd = handleAgentClickedMsg(m, msg)
	case mcpToolsRegisteredMsg:
		m, cmd = handleMCPToolsRegisteredMsg(m, msg)
	case tokenCountUpdateMsg:
		m, cmd = handleTokenCountUpdateMsg(m, msg)
	case zulip.MessageReceivedMsg:
		m, cmd = handleZulipMessageReceivedMsg(m, msg)
	case zulip.ConnectionStatusMsg:
		m, cmd = handleZulipConnectionStatusMsg(m, msg)
	case zulip.ReactionReceivedMsg:
		m, cmd = handleZulipReactionMsg(m, msg)
	case zulipPermissionPromptSentMsg:
		m.pendingZulipPermissions[msg.MessageID] = PendingZulipPermission{
			Request:          msg.Request,
			AuthorizedUserID: msg.AuthorizedUserID,
		}
		return m, nil
	case permissionRequestMsg:
		m, cmd = handlePermissionRequestMsg(m, msg)
	case permissionMentionMsg:
		m, cmd = handlePermissionMentionMsg(m, msg)
	// Remote agent (agentd) messages
	case agentConnectedMsg:
		m, cmd = handleAgentConnectedMsg(m, msg)
	case agentDisconnectedMsg:
		m, cmd = handleAgentDisconnectedMsg(m, msg)
	case agentEventMsg:
		m, cmd = handleAgentEventMsg(m, msg)
	default:
		cmd = nil
	}

	return m, tea.Batch(tiCmd, vpCmd, spCmd, zulipStartCmd, cmd)
}

// handleKeyMsg routes keyboard messages to specific key handlers
func (m Model) handleKeyMsg(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return handleKeyEsc(m)
	case tea.KeyCtrlC:
		return handleKeyCtrlC(m)
	case tea.KeyCtrlH:
		return handleKeyCtrlH(m)
	case tea.KeyCtrlA:
		return handleKeyCtrlA(m)
	case tea.KeyCtrlQ:
		return handleKeyCtrlQ(m)
	case tea.KeyCtrlV:
		return handleKeyCtrlV(m)
	case tea.KeyEnter:
		return handleKeyEnter(m)
	default:
		return m, nil
	}
}

// Helper methods

func (m *Model) sendMessageToAgent(content string) (tea.Model, tea.Cmd) {
	agentID := m.viewingAgentID
	agent, exists := m.agentManager.GetAgent(agentID)
	if !exists {
		m.err = fmt.Errorf("agent %s not found", agentID)
		return m, nil
	}

	if activeAgent, ok := m.activeAgents[agentID]; ok {
		activeAgent.Messages = append(activeAgent.Messages, "👤 You:\n"+content)
	}

	userMsg := agents.Message{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		AgentID:   agentID,
		Type:      agents.MessageTypeQuestion,
		Content:   content,
		Timestamp: time.Now(),
	}

	if err := agent.SendMessage(userMsg); err != nil {
		m.err = err
		return m, nil
	}

	m.textarea.Reset()
	m.clearTextareaBuffer(agentID)
	m.updateViewport()
	return m, nil
}

func (m *Model) sendToLLM() tea.Cmd {
	return func() tea.Msg {
		stream := m.streamFromLLM()
		return streamStartMsg{stream: stream}
	}
}

func (m *Model) streamFromLLM() <-chan llm.StreamChunk {
	ctx := context.Background()
	apiMessages := []chat.Message{}

	// Determine the LLM context (system prompt + tools) for this call.
	// For Zulip messages the Zulip persona is applied; for all other messages
	// the pre-stored m.activeTools / main-chat system prompt is used.
	var tools []llm.Tool
	if m.respondingToZulip && m.config != nil {
		zulipCtx := app.BuildLLMContext(
			m.config,
			m.skillRegistry,
			m.promptRegistry,
			m.personaRegistry,
			m.config.Zulip.Persona,
			nil,
		)
		// Prepend the persona system prompt; existing system messages are skipped below.
		apiMessages = append(apiMessages, chat.NewSystemMessage(zulipCtx.SystemPrompt))
		tools = zulipCtx.Tools
	} else {
		// Main chat: use the stored tool list (consistent with m.messages[0] system prompt).
		tools = m.activeTools
	}

	for i, msg := range m.messages {
		if msg.Role == chat.RoleSummary || msg.Role == chat.RoleNote {
			continue
		}
		if msg.Role == chat.RoleSystem && m.respondingToZulip {
			// We already prepended the persona system prompt above; skip stored one.
			continue
		}
		if m.isStreaming && i == m.streamingMsgIndex {
			continue
		}
		if msg.Role == chat.RoleTool {
			apiMsg := chat.NewUserMessage(fmt.Sprintf("[Tool result for %s]: %s", msg.ToolName, msg.Content))
			apiMessages = append(apiMessages, apiMsg)
		} else {
			apiMessages = append(apiMessages, msg)
		}
	}

	stream, err := m.llmClient.StreamMessage(ctx, apiMessages, tools)
	if err != nil {
		errChan := make(chan llm.StreamChunk, 1)
		errChan <- llm.StreamChunk{Error: err}
		close(errChan)
		return errChan
	}
	return stream
}

func (m *Model) mockResponse(userMsg string) tea.Cmd {
	return func() tea.Msg {
		response := "This is a mock response to: " + userMsg + "\n\n(LLM client not configured)"
		return llmResponseMsg{content: response}
	}
}


func (m *Model) updateViewport() {
	var content string
	if m.viewingAgentID != "" {
		content = m.renderAgentConversation(m.viewingAgentID)
	} else if m.isStreaming && m.stableRenderedCache != "" && m.streamingMsgIndex == m.stableCacheCount {
		// Fast path: during streaming, only re-render the single in-progress
		// message and concatenate with the pre-rendered stable prefix.
		content = m.stableRenderedCache + m.renderSingleMessage(m.streamingMsgIndex)
	} else {
		content = m.renderMessages()
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *Model) handleCommand(cmdLine string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		return m, nil
	}

	cmdName := strings.TrimPrefix(parts[0], "/")
	args := parts[1:]

	cmd, ok := m.skillRegistry.GetCommand(cmdName)
	if !ok {
		m.err = fmt.Errorf("unknown command: %s", cmdName)
		m.messages = append(m.messages,
			chat.NewSystemMessage(fmt.Sprintf("Error: Unknown command /%s", cmdName)))
		m.updateViewport()
		return m, m.updateTokenCountAsync()
	}

	return m, func() tea.Msg {
		state := &skills.CommandState{
			Messages:      m.messages,
			Config:        m.config,
			LLMClient:     m.llmClient,
			TraceFilePath: m.tracer.FilePath(),
			AgentClient:   m.agentClient,
			SessionID:     m.sessionID,
		}

		result, err := cmd.Execute(context.Background(), args, state)
		if err != nil {
			return errMsg{err: err}
		}
		return commandExecutedMsg{result: result}
	}
}

func (m *Model) agentPanelRefreshTicker() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return agentPanelRefreshMsg{}
	})
}

func (m *Model) saveCurrentTextareaBuffer() {
	m.textareaBuffers[m.currentContext] = m.textarea.Value()
}

func (m *Model) loadTextareaBuffer(contextID string) {
	m.currentContext = contextID
	if content, exists := m.textareaBuffers[contextID]; exists {
		m.textarea.SetValue(content)
	} else {
		m.textarea.Reset()
	}
}

func (m *Model) clearTextareaBuffer(contextID string) {
	delete(m.textareaBuffers, contextID)
}

func (m Model) quit() tea.Cmd {
	if m.tracer != nil {
		if err := m.tracer.Close(); err != nil {
			logging.Warnf("Failed to close tracer: %v", err)
		}
	}
	if m.agentUnsubscribe != nil {
		m.agentUnsubscribe()
	}
	return tea.Quit
}

func (m *Model) updateTokenCountAsyncForAgentContext(agentID string) tea.Cmd {
	if m.agentManager == nil {
		return nil
	}

	agent, ok := m.agentManager.GetAgent(agentID)
	if !ok {
		return nil
	}

	baseAgent, ok := agent.(*agents.BaseAgent)
	if !ok {
		return nil
	}

	messages := baseAgent.GetConversation()
	return m.updateTokenCountAsyncForContext(agentID, messages)
}

type llmResponseMsg struct {
	content string
}


func waitForStreamChunk(stream <-chan llm.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-stream
		if !ok {
			return streamDoneMsg{}
		}

		if chunk.Error != nil {
			return errMsg{err: chunk.Error}
		}

		if chunk.Done {
			return streamDoneMsg{}
		}

		return streamChunkMsg{
			content:  chunk.Content,
			thinking: chunk.Thinking,
			toolUse:  chunk.ToolUse,
			stream:   stream,
		}
	}
}

func checkCompactionNeeded(m *Model) tea.Cmd {
	if m.compactor == nil || m.tokenCounter == nil {
		return nil
	}

	return func() tea.Msg {
		count, err := m.tokenCounter.CountMessages(m.messages)
		if err != nil {
			return nil
		}

		threshold := m.config.Compaction.CompactThreshold
		if count > threshold {
			return compactionNeededMsg{
				currentTokens: count,
				threshold:     threshold,
			}
		}
		return nil
	}
}

func performCompaction(m *Model) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		newMessages, err := m.compactor.Compact(ctx, m.messages)
		if err != nil {
			return compactionErrorMsg{err: err}
		}

		summary := chat.NewSummaryMessage("Conversation compacted. Earlier messages summarized.")
		return compactionDoneMsg{
			newMessages: newMessages,
			summary:     summary,
		}
	}
}

func executeTool(m *Model, toolUse llm.ToolUse) tea.Cmd {
	return func() tea.Msg {
		tool, ok := m.skillRegistry.GetTool(toolUse.Name)
		if !ok {
			stream := m.streamFromLLM()
			return toolResultMsg{
				toolUseID: toolUse.ID,
				toolName:  toolUse.Name,
				result: skills.ToolResult{
					Success: false,
					Error:   fmt.Sprintf("unknown tool: %s", toolUse.Name),
				},
				stream: stream,
			}
		}

		result, err := tool.Execute(context.Background(), toolUse.Parameters)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
		}

		stream := m.streamFromLLM()
		return toolResultMsg{
			toolUseID: toolUse.ID,
			toolName:  toolUse.Name,
			result:    result,
			stream:    stream,
		}
	}
}
