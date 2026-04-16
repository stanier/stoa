package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/permissions"
)

// agentPermRequest is a minimal stand-in for permissions.PermissionRequest used by the
// remote permission prompt. It carries enough information to display the modal without
// needing the local permissions package's channel-based response mechanism.
type agentPermRequest struct {
	toolName    string
	serverName  string
	description string
	parameters  map[string]interface{}
}

// permRequestFromAgentEvent converts an agentproto.PermRequestEvent into a local
// permissions.PermissionRequest so the existing modal UI can render it.
// The ResponseChan is left nil because agentd permission responses go over the WebSocket.
func permRequestFromAgentEvent(ev *agentproto.PermRequestEvent) *permissions.PermissionRequest {
	params := make(map[string]interface{})
	if len(ev.Params) > 0 {
		_ = json.Unmarshal(ev.Params, &params)
	}
	return &permissions.PermissionRequest{
		ToolName:    ev.ToolName,
		Description: ev.Description,
		Parameters:  params,
		// ResponseChan intentionally nil — we use agentConn instead.
	}
}

// AgentPermissionPromptModel is a variant of PermissionPromptModel that sends the
// user's decision back to agentd over the WebSocket connection rather than through
// a local channel.
type AgentPermissionPromptModel struct {
	request   *permissions.PermissionRequest
	conn      agentproto.ClientConn
	requestID string

	// UI state (mirrors PermissionPromptModel)
	focused  int
	remember bool
	width    int
	height   int

	// dismissed is set to true once the user has responded.
	dismissed bool
}

// NewPermissionPromptModelFromAgent creates an AgentPermissionPromptModel.
func NewPermissionPromptModelFromAgent(
	request *permissions.PermissionRequest,
	conn agentproto.ClientConn,
	requestID string,
) *PermissionPromptModel {
	// We piggyback on PermissionPromptModel's rendering but override the response
	// mechanism via the agentConn stored in this wrapper. Since PermissionPromptModel
	// is what the main model checks (m.activePermissionPrompt), we adapt here.
	// The actual response-sending is done by hijacking PermissionPromptModel.request:
	// we supply a fake request with a real ResponseChan that a goroutine bridges to conn.
	responseChan := make(chan permissions.PermissionResponse, 1)

	bridgedReq := &permissions.PermissionRequest{
		ToolName:     request.ToolName,
		ServerName:   request.ServerName,
		Description:  request.Description,
		Parameters:   request.Parameters,
		ResponseChan: responseChan,
	}

	// Bridge: wait for user's local response then forward it over WebSocket.
	go func() {
		resp, ok := <-responseChan
		if !ok {
			return
		}
		_ = conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{
				RequestID: requestID,
				Approved:  resp.Approved,
			},
		})
	}()

	return &PermissionPromptModel{
		request:  bridgedReq,
		focused:  1,
		remember: false,
	}
}

// renderAgentPermissionModal is a standalone View for AgentPermissionPromptModel
// (used as a fallback if the bridged approach above isn't sufficient).
func (m *AgentPermissionPromptModel) View() string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Padding(0, 1)
	b.WriteString(titleStyle.Render("Permission Request (agentd)"))
	b.WriteString("\n\n")

	infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	b.WriteString(infoStyle.Render(fmt.Sprintf("Tool: %s", m.request.ToolName)))
	b.WriteString("\n")
	if m.request.ServerName != "" {
		b.WriteString(infoStyle.Render(fmt.Sprintf("Server: %s", m.request.ServerName)))
		b.WriteString("\n")
	}
	b.WriteString(infoStyle.Render(fmt.Sprintf("Description: %s", m.request.Description)))
	b.WriteString("\n\n")

	denyStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("9")).Border(lipgloss.RoundedBorder(), true)
	allowStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color("10")).Border(lipgloss.RoundedBorder(), true)
	selectedStyle := lipgloss.NewStyle().Padding(0, 2).Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12")).Border(lipgloss.RoundedBorder(), true)

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
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true).Render("y: approve • n: deny"))

	boxStyle := lipgloss.NewStyle().Border(lipgloss.DoubleBorder(), true).BorderForeground(lipgloss.Color("12")).Background(lipgloss.Color("235")).Padding(1, 2).Width(70)
	return boxStyle.Render(b.String())
}

// Update handles key events for AgentPermissionPromptModel.
func (m *AgentPermissionPromptModel) Update(msg tea.Msg) (*AgentPermissionPromptModel, tea.Cmd) {
	if m.dismissed {
		return m, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.String() {
	case "tab", "right", "down":
		m.focused = (m.focused + 1) % 2
	case "shift+tab", "left", "up":
		m.focused = (m.focused + 1) % 2
	case "enter":
		approved := m.focused == 1
		m.dismiss(approved)
		return m, waitForEvent(m.conn.Events())
	case "y", "Y":
		m.dismiss(true)
		return m, waitForEvent(m.conn.Events())
	case "n", "N":
		m.dismiss(false)
		return m, waitForEvent(m.conn.Events())
	}
	return m, nil
}

func (m *AgentPermissionPromptModel) dismiss(approved bool) {
	m.dismissed = true
	_ = m.conn.Send(agentproto.ClientMessage{
		Type: agentproto.MessageTypePermResponse,
		PermResponse: &agentproto.PermResponsePayload{
			RequestID: m.requestID,
			Approved:  approved,
		},
	})
}

// SetSize sets the viewport dimensions.
func (m *AgentPermissionPromptModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}
