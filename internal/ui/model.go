package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/agents"
	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/compaction"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/mcp"
	"github.com/stanier/stoa/internal/permissions"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
	"github.com/stanier/stoa/internal/skills/builtin"
	"github.com/stanier/stoa/internal/trace"
	"github.com/stanier/stoa/internal/zulip"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Model represents the application state
type Model struct {
	// Configuration
	config *config.Config

	// LLM client
	llmClient llm.Client

	// UI components
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	// Chat state
	messages []chat.Message

	// Streaming state
	isStreaming       bool
	currentStreamMsg  []byte
	streamingMsgIndex int
	currentThinking   []byte // Accumulates thinking text during streaming

	// Render cache for streaming performance.
	// stableRenderedCache holds the fully-rendered output of all messages
	// before the streaming message so that each chunk only re-renders the
	// single in-progress message rather than the entire conversation.
	// Invalidated (set to "") whenever messages change outside streaming.
	stableRenderedCache string
	stableCacheCount    int // number of messages that are captured in the cache

	// Compaction
	compactor       compaction.Compactor
	tokenCounter    compaction.TokenCounter
	needsCompaction bool

	// Token counting for display
	cachedTokenCount int
	tokenCountDirty  bool
	tokenCountCache  map[string]int // Maps context ID ("" = main, agentID = agent) to token count

	// Real token counts from last LLM API response (remote/agentd mode)
	lastTurnInputTokens  int
	lastTurnOutputTokens int

	// Skills
	skillRegistry   *skills.Registry
	agentController skills.AgentController // nil until agent management is available

	// Prompts
	promptRegistry *prompts.Registry

	// MCP
	mcpManager   *mcp.ServerManager
	mcpReadyChan chan tea.Msg  // receives mcpToolsRegisteredMsg when background connect finishes
	activeTools  []llm.Tool   // tool list derived from the same filtered view as the system prompt

	// Agents
	agentManager          *agents.Manager
	personaRegistry       *personas.Registry
	activeAgents          map[string]*AgentView
	showAgentPanel        bool
	firstSpawnSinceLaunch bool
	agentMsgChan          chan tea.Msg
	agentUnsubscribe      func() // closes the agent manager subscription goroutine on quit
	viewingAgentID        string                       // empty = main chat, non-empty = viewing agent
	agentCardPositions    map[string]AgentCardPosition // click zones

	// Input buffers per context
	textareaBuffers map[string]string // Maps context ID to draft content ("" = main, agentID = agent)
	currentContext  string            // Current input context

	// UI state
	ready            bool
	width            int
	height           int
	err              error
	showHelp         bool
	focused          bool // Track whether the application is in focus
	markdownRenderer *MarkdownRenderer

	// Trace
	tracer *trace.Tracer

	// Permissions
	permissionChecker       *permissions.DefaultChecker
	permissionManager       *permissions.DefaultManager
	permissionAuditLogger   *permissions.AuditLogger
	permissionRequestChan   chan *permissions.PermissionRequest
	permissionMentionChan   chan *permissions.PermissionMention
	activePermissionPrompt  *PermissionPromptModel
	pendingZulipPermissions map[int]PendingZulipPermission // Key: Zulip prompt message ID

	// Tool result expansion tracking
	expandedToolResults  map[int]bool      // Message index -> expanded state
	toolResultClickZones map[int]ClickZone // Message index -> click zone for expansion
	lastClickTime        time.Time         // Track for double-click detection
	lastClickIndex       int               // Track last clicked message index

	// Zulip integration
	zulipBridge       *zulip.Bridge
	zulipMessages     []ZulipMessageContext
	zulipMsgChan      chan tea.Msg
	respondingToZulip bool // Flag to indicate if current response is for Zulip

	// Remote agent (agentd) connection — used when running as a thin client.
	// When conn is non-nil the model delegates all LLM/tool/MCP work to agentd.
	conn        agentproto.ClientConn
	agentClient agentproto.Client // non-nil in remote mode; HTTP client for GetTokens etc.
	sessionID   string
	agentAddr   string // base URL of agentd, e.g. "http://localhost:7777"

	// Ctrl+C double-tap quit: first press arms it, second press within the window quits.
	ctrlCPending bool
}

// PendingZulipPermission tracks a permission request sent to Zulip
type PendingZulipPermission struct {
	Request          *permissions.PermissionRequest
	AuthorizedUserID int
}

// ClickZone represents a clickable area for tool results
type ClickZone struct {
	MessageIndex int
	StartY       int
	EndY         int
}

// ZulipMessageContext tracks the relationship between Zulip and conversation messages
type ZulipMessageContext struct {
	ZulipMsgID   int
	ConvIndex    int
	Responded    bool
	BridgeReply  chan<- zulip.MessageResponse
	ZulipMessage *zulip.Message
}

func New() Model {
	return NewWithConfig(nil)
}


func setupTokenCounter(cfg *config.Config) compaction.TokenCounter {
	if cfg == nil {
		return nil
	}

	// TODO: Replace with server-returned token counts once the LLM client propagates
	// usage statistics (input_tokens / output_tokens) from API responses.
	// Using a fast character-based estimate for now to avoid tiktoken's
	// first-call encoding load latency.
	return compaction.NewCharEstimateCounter()
}

func setupCompaction(cfg *config.Config, llmClient llm.Client, tokenCounter compaction.TokenCounter, promptRegistry *prompts.Registry) compaction.Compactor {
	if cfg == nil || !cfg.Compaction.Enabled || tokenCounter == nil || llmClient == nil {
		return nil
	}

	strategy := compaction.Strategy(compaction.NewSlidingWindowStrategy(cfg.Compaction.KeepRecent))
	switch cfg.Compaction.Strategy {
	case "sliding_window": // explicit — uses the default above
	case "importance":
		strategy = compaction.NewImportanceBasedStrategy(cfg.Compaction.KeepRecent)
	default:
		logging.Warnf("Unrecognised compaction strategy %q, falling back to sliding_window", cfg.Compaction.Strategy)
	}

	return compaction.NewDefaultCompactorWithPrompts(
		llmClient,
		tokenCounter,
		strategy,
		cfg.Compaction.MaxTokens,
		cfg.Compaction.CompactThreshold,
		promptRegistry,
	)
}



// setupMCPManager creates and connects MCP servers in the background.
// Returns the manager and a channel that receives mcpToolsRegisteredMsg when
// the async connection + discovery finishes (regardless of success/failure).
func setupMCPManager(cfg *config.Config, skillRegistry *skills.Registry, permSetup *app.PermissionsSetup) (*mcp.ServerManager, chan tea.Msg) {
	readyChan := make(chan tea.Msg, 1)

	opts := app.MCPSetupOptions{
		ConnectTimeout: 10 * time.Second,
		OnComplete: func(count int) {
			readyChan <- mcpToolsRegisteredMsg{count: count}
		},
	}
	if permSetup != nil {
		opts.PermChecker = permSetup.Checker
		opts.AuditLogger = permSetup.AuditLogger
		opts.PermReqChan = permSetup.RequestChan
		opts.PermMentionChan = permSetup.MentionChan
	}

	manager := app.SetupMCPManager(cfg, skillRegistry, opts)
	if manager == nil {
		return nil, nil
	}
	return manager, readyChan
}

func setupAgentManager(cfg *config.Config, llmClient llm.Client, skillRegistry *skills.Registry, promptRegistry *prompts.Registry) (*agents.Manager, *agents.ManagerController, *personas.Registry, chan tea.Msg, func()) {
	if cfg == nil || !cfg.Agents.Enabled || llmClient == nil {
		return nil, nil, nil, nil, nil
	}

	agentManager := agents.NewManagerWithConfig(llmClient, skillRegistry, promptRegistry, cfg, cfg.Agents.MaxConcurrent)

	personaRegistry := app.SetupPersonaRegistry()
	agentManager.SetPersonaRegistry(personaRegistry)

	controller := agents.NewManagerController(agentManager)

	agentMsgChan := make(chan tea.Msg, 100)

	skillRegistry.RegisterCommand(builtin.NewSpawnCommand())
	skillRegistry.RegisterCommand(builtin.NewAgentsCommand())
	skillRegistry.RegisterCommand(builtin.NewKillCommand())

	msgChan, unsubscribe := agentManager.Subscribe()
	go func() {
		for msg := range msgChan {
			switch msg.Type {
			case "result":
				agentMsgChan <- agentCompletedMsg{
					agentID: msg.AgentID,
					content: msg.Content,
				}
			case "error":
				agentMsgChan <- agentErrorMsg{
					agentID: msg.AgentID,
					err:     fmt.Errorf("%s", msg.Content),
				}
			default:
				agentMsgChan <- agentMessageMsg{
					agentID: msg.AgentID,
					message: msg,
				}
			}
		}
	}()

	return agentManager, controller, personaRegistry, agentMsgChan, unsubscribe
}

func setupMarkdownRenderer() *MarkdownRenderer {
	renderer, err := NewMarkdownRenderer(80)
	if err != nil {
		logging.Warnf("Failed to initialize Markdown renderer: %v", err)
		return nil
	}
	return renderer
}

func setupZulipBridge(cfg *config.Config, msgChan chan tea.Msg) *zulip.Bridge {
	// Check if Zulip is enabled in config AND the --enable-connectors flag is set
	if cfg == nil || !cfg.Zulip.Enabled || !cfg.EnableConnectorsFlag {
		return nil
	}

	// Validate required Zulip configuration
	if cfg.Zulip.SiteURL == "" || cfg.Zulip.Email == "" || cfg.Zulip.APIKey == "" {
		return nil
	}

	return zulip.NewBridge(zulip.BridgeConfig{
		SiteURL:         cfg.Zulip.SiteURL,
		Email:           cfg.Zulip.Email,
		APIKey:          cfg.Zulip.APIKey,
		EventTypes:      cfg.Zulip.EventTypes,
		UseReactions:    cfg.Zulip.UseReactions,
		EmergencyCutoff: cfg.Zulip.EmergencyCutoff,
		PollInterval:    1 * time.Second,
	}, msgChan)
}



func NewWithConfig(cfg *config.Config) Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	vp := viewport.New(80, 20)

	llmClient := app.SetupLLMClient(cfg)
	tokenCounter := setupTokenCounter(cfg)
	skillRegistry := app.SetupSkillRegistry(cfg, "")
	promptRegistry := app.SetupPromptRegistry(cfg)
	compactor := setupCompaction(cfg, llmClient, tokenCounter, promptRegistry)
	permSetup := app.SetupPermissions(cfg)
	mcpManager, mcpReadyChan := setupMCPManager(cfg, skillRegistry, permSetup)
	agentManager, agentCtrl, personaRegistry, agentMsgChan, agentUnsubscribe := setupAgentManager(cfg, llmClient, skillRegistry, promptRegistry)
	mdRenderer := setupMarkdownRenderer()

	zulipMsgChan := make(chan tea.Msg, 10)
	zulipBridge := setupZulipBridge(cfg, zulipMsgChan)
	tracer := app.SetupTracer(cfg, "")

	m := Model{
		config:                  cfg,
		llmClient:               llmClient,
		textarea:                ta,
		spinner:                 sp,
		viewport:                vp,
		messages:                []chat.Message{},
		isStreaming:             false,
		streamingMsgIndex:       -1,
		compactor:               compactor,
		tokenCounter:            tokenCounter,
		needsCompaction:         false,
		cachedTokenCount:        0,
		tokenCountDirty:         true,
		tokenCountCache:         make(map[string]int),
		skillRegistry:           skillRegistry,
		agentController:         agentCtrl,
		promptRegistry:          promptRegistry,
		mcpManager:              mcpManager,
		mcpReadyChan:            mcpReadyChan,
		agentManager:            agentManager,
		personaRegistry:         personaRegistry,
		activeAgents:            make(map[string]*AgentView),
		showAgentPanel:          false,
		firstSpawnSinceLaunch:   true,
		agentMsgChan:            agentMsgChan,
		agentUnsubscribe:        agentUnsubscribe,
		viewingAgentID:          "",
		agentCardPositions:      make(map[string]AgentCardPosition),
		textareaBuffers:         make(map[string]string),
		currentContext:          "",
		ready:                   false,
		expandedToolResults:     make(map[int]bool),
		toolResultClickZones:    make(map[int]ClickZone),
		lastClickIndex:          -1,
		showHelp:                false,
		focused:                 true,
		markdownRenderer:        mdRenderer,
		zulipBridge:             zulipBridge,
		zulipMessages:           []ZulipMessageContext{},
		zulipMsgChan:            zulipMsgChan,
		tracer:                  tracer,
		pendingZulipPermissions: make(map[int]PendingZulipPermission),
	}

	// Initialize permission system if enabled
	if permSetup != nil {
		m.permissionChecker = permSetup.Checker
		m.permissionManager = permSetup.Manager
		m.permissionAuditLogger = permSetup.AuditLogger
		m.permissionRequestChan = permSetup.RequestChan
		m.permissionMentionChan = permSetup.MentionChan
		m.activePermissionPrompt = nil
	}

	// Build initial LLM context (system prompt + tools) using the configured persona.
	// Set ui.persona: "" to bypass persona filtering and expose all tools.
	// MCP tools aren't connected yet — they will trigger a rebuild via mcpToolsRegisteredMsg.
	tuiPersona := ""
	if cfg != nil {
		tuiPersona = cfg.UI.Persona
	}
	initialCtx := app.BuildLLMContext(cfg, m.skillRegistry, m.promptRegistry, m.personaRegistry, tuiPersona, nil)
	m.messages = append(m.messages, chat.NewSystemMessage(initialCtx.SystemPrompt))
	m.activeTools = initialCtx.Tools

	// Add welcome message with model name
	modelName := "default"
	if cfg != nil {
		if dp := cfg.LLM.DefaultProvider(); dp != nil && dp.Model != "" {
			modelName = dp.Model
		}
	}
	welcomeMsg := fmt.Sprintf("Welcome to Stoa! Using model: %s", modelName)
	m.messages = append(m.messages, chat.NewNoteMessage(welcomeMsg))

	m.tokenCountDirty = true

	return m
}

// NewResumed creates a new model initialized with the provided messages (for session resume)
func NewResumed(cfg *config.Config, messages []chat.Message) Model {
	m := NewWithConfig(cfg)
	if len(messages) > 0 {
		m.messages = messages
		m.tokenCountDirty = true
		// Add a separation marker
		m.messages = append(m.messages, chat.NewNoteMessage("--- Session Resumed ---"))
	}
	return m
}

// initialModel creates the base UI model with only UI components initialised.
// It does not set up LLM clients, MCP managers, or any "brain" components.
func initialModel() Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	vp := viewport.New(80, 20)
	mdRenderer := setupMarkdownRenderer()

	return Model{
		textarea:              ta,
		spinner:               sp,
		viewport:              vp,
		messages:              []chat.Message{},
		isStreaming:           false,
		streamingMsgIndex:     -1,
		cachedTokenCount:      0,
		tokenCountDirty:       true,
		tokenCountCache:       make(map[string]int),
		activeAgents:          make(map[string]*AgentView),
		showAgentPanel:        false,
		firstSpawnSinceLaunch: true,
		agentCardPositions:    make(map[string]AgentCardPosition),
		textareaBuffers:       make(map[string]string),
		currentContext:        "",
		ready:                 false,
		expandedToolResults:   make(map[int]bool),
		toolResultClickZones:  make(map[int]ClickZone),
		lastClickIndex:        -1,
		showHelp:              false,
		focused:               true,
		markdownRenderer:      mdRenderer,
		zulipMessages:         []ZulipMessageContext{},
		pendingZulipPermissions: make(map[int]PendingZulipPermission),
	}
}

// NewRemote creates a thin-client Model that connects to agentd at the address
// specified in cfg.Agent.Address (default "http://localhost:7777").
// It does not create any LLM client, MCP manager, or skills registry —
// all of that logic lives in agentd.
func NewRemote(cfg *config.Config) (Model, error) {
	addr := "http://localhost:7777"
	if cfg != nil && cfg.Agent.Address != "" {
		addr = cfg.Agent.Address
	}

	m := initialModel()
	m.config = cfg
	m.agentAddr = addr
	m.skillRegistry = app.SetupSkillRegistry(cfg, "")

	// Add welcome message
	m.messages = append(m.messages, chat.NewNoteMessage(
		fmt.Sprintf("Connecting to agentd at %s...", addr),
	))

	return m, nil
}

// NewWithLocalAgent creates a Model backed by a pre-built agentproto.Client
// (typically an agentd.LocalClient running in-process). No network address is
// needed; the client handles session creation and event delivery directly.
func NewWithLocalAgent(cfg *config.Config, client agentproto.Client) (Model, error) {
	m := initialModel()
	m.config = cfg
	m.agentClient = client
	m.skillRegistry = app.SetupSkillRegistry(cfg, "")
	// agentAddr is set to a sentinel so Init triggers the remote-mode connection
	// path, but connectWithClient is used instead of connectToAgent.
	m.agentAddr = "local"

	m.messages = append(m.messages, chat.NewNoteMessage("Starting local agent..."))
	return m, nil
}

// connectToAgent dials agentd: creates a session then opens the WebSocket.
func connectToAgent(addr string, persona string, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		apiKey := ""
		tlsSkipVerify := false
		if cfg != nil {
			apiKey = cfg.Agent.APIKey
			tlsSkipVerify = cfg.Agent.TLSSkipVerify
		}
		client := agentproto.NewWSClientWithOptions(addr, apiKey, tlsSkipVerify)
		ctx := context.Background()
		sessionID, err := client.CreateSession(ctx, agentproto.SessionConfig{Persona: persona})
		if err != nil {
			return agentDisconnectedMsg{err: fmt.Errorf("create session: %w", err)}
		}
		conn, err := client.Connect(ctx, sessionID)
		if err != nil {
			return agentDisconnectedMsg{err: fmt.Errorf("connect ws: %w", err)}
		}
		return agentConnectedMsg{conn: conn, client: client, sessionID: sessionID}
	}
}

// connectWithClient uses a pre-built agentproto.Client (e.g. LocalClient) to
// create a session and open an in-process connection. Used by NewWithLocalAgent.
func connectWithClient(client agentproto.Client, persona string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		sessionID, err := client.CreateSession(ctx, agentproto.SessionConfig{
			Persona: persona,
			Source:  "tui",
		})
		if err != nil {
			return agentDisconnectedMsg{err: fmt.Errorf("create session: %w", err)}
		}
		conn, err := client.Connect(ctx, sessionID)
		if err != nil {
			return agentDisconnectedMsg{err: fmt.Errorf("connect: %w", err)}
		}
		return agentConnectedMsg{conn: conn, client: client, sessionID: sessionID}
	}
}

// reconnectToSession closes the current agentd connection and connects to an
// already-existing session by ID. Used after /persona or /model creates a branch.
func (m Model) reconnectToSession(sessionID, noteMsg string) (Model, tea.Cmd) {
	// Close the existing connection cleanly.
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}

	// Reset conversation state; the branched session has its own history.
	m.messages = nil
	m.isStreaming = false
	m.streamingMsgIndex = -1
	m.stableRenderedCache = ""

	if noteMsg != "" {
		m.messages = append(m.messages, chat.NewNoteMessage(noteMsg))
	}
	m.updateViewport()

	client := m.agentClient
	return m, func() tea.Msg {
		ctx := context.Background()
		conn, err := client.Connect(ctx, sessionID)
		if err != nil {
			return agentDisconnectedMsg{err: fmt.Errorf("connect to branch %s: %w", sessionID, err)}
		}
		return agentConnectedMsg{conn: conn, client: client, sessionID: sessionID}
	}
}

// waitForEvent reads one event from the agentd connection channel and returns it
// as an agentEventMsg. When the channel is closed it returns agentDisconnectedMsg.
func waitForEvent(ch <-chan agentproto.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentDisconnectedMsg{err: fmt.Errorf("agentd connection closed")}
		}
		return agentEventMsg{event: ev}
	}
}

// updateTokenCount recalculates and caches the token count
func (m *Model) updateTokenCount() {
	if m.tokenCounter == nil {
		m.tokenCountDirty = false
		return
	}

	count, err := m.tokenCounter.CountMessages(m.messages)
	if err == nil {
		m.cachedTokenCount = count
	}
	m.tokenCountDirty = false
}

// getRoughEstimate calculates a quick token count estimate
func (m *Model) getRoughEstimate() int {
	total := 0
	for _, msg := range m.messages {
		total += 4 + len(msg.Content)/4 // 4 tokens overhead + text estimate
	}
	return total
}

// agentPanelWidth calculates the width of the agent panel
// Returns 20% of terminal width with a minimum of 25 characters
func (m *Model) agentPanelWidth() int {
	panelWidth := m.width / 5
	if panelWidth < 25 {
		panelWidth = 25
	}
	return panelWidth
}

// updateTokenCountAsync updates token count asynchronously without blocking the UI
// Returns a tea.Cmd that will send tokenCountUpdateMsg when done
func (m *Model) updateTokenCountAsync() tea.Cmd {
	// Get messages for the current context
	contextID := m.viewingAgentID
	var messages []chat.Message

	if contextID == "" {
		// Main conversation
		messages = m.messages
	} else {
		// Agent conversation
		if m.agentManager != nil {
			if agent, ok := m.agentManager.GetAgent(contextID); ok {
				// BaseAgent has GetConversation method
				if baseAgent, ok := agent.(*agents.BaseAgent); ok {
					messages = baseAgent.GetConversation()
				}
			}
		}
		// If agent not found or has no messages, use empty list (don't fall back to main)
	}

	return m.updateTokenCountAsyncForContext(contextID, messages)
}

// updateTokenCountAsyncForContext updates token count for a specific context
// contextID: "" for main chat, agentID for agent conversation
// messages: the messages to count (for the specified context)
func (m *Model) updateTokenCountAsyncForContext(contextID string, messages []chat.Message) tea.Cmd {
	// Calculate rough estimate
	rough := 0
	for _, msg := range messages {
		rough += 4 + len(msg.Content)/4
	}

	// Store rough estimate immediately
	m.tokenCountCache[contextID] = rough
	if contextID == m.viewingAgentID {
		m.cachedTokenCount = rough
		m.tokenCountDirty = false
	}

	// Capture messages for async operation
	msgs := make([]chat.Message, len(messages))
	copy(msgs, messages)

	return func() tea.Msg {
		if m.tokenCounter == nil {
			return nil // Already set rough estimate
		}

		count, err := m.tokenCounter.CountMessages(msgs)
		if err != nil {
			return nil // Keep rough estimate
		}

		return tokenCountUpdateMsg{contextID: contextID, count: count}
	}
}

// AgentView represents the agent view state
type AgentView struct {
	ID       string
	Persona  string
	Name     string
	Status   agents.Status
	Messages []string
	Progress float64
	Task     string
}

// AgentCardPosition tracks the click zone for an agent card
type AgentCardPosition struct {
	AgentID    string
	StartY     int // Starting Y position in terminal coordinates
	EndY       int // Ending Y position in terminal coordinates
	PanelX     int // X offset where panel starts
	PanelWidth int // Width of the panel
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spinner.Tick,
	}

	// Only blink cursor when focused
	if m.focused {
		cmds = append(cmds, textarea.Blink)
	}

	// Remote mode: connect to agentd instead of setting up local LLM.
	if m.agentAddr != "" && m.conn == nil {
		persona := ""
		if m.config != nil {
			persona = m.config.UI.Persona
		}
		if m.agentClient != nil {
			// Pre-built client (e.g. LocalClient) — skip network dialing.
			cmds = append(cmds, connectWithClient(m.agentClient, persona))
		} else {
			cmds = append(cmds, connectToAgent(m.agentAddr, persona, m.config))
		}
		return tea.Batch(cmds...)
	}

	// Wait for MCP background connect to finish so we can rebuild the system prompt
	if m.mcpReadyChan != nil {
		cmds = append(cmds, waitForMCPReady(m.mcpReadyChan))
	}

	// Add agent message poller if agent manager is enabled
	if m.agentMsgChan != nil {
		cmds = append(cmds, waitForAgentMsg(m.agentMsgChan))
	}

	// Add permission request poller if permissions are enabled
	if m.permissionRequestChan != nil {
		cmds = append(cmds, waitForPermissionRequest(m.permissionRequestChan))
	}

	// Add permission mention poller if permissions are enabled
	if m.permissionMentionChan != nil {
		cmds = append(cmds, waitForPermissionMention(m.permissionMentionChan))
	}

	return tea.Batch(cmds...)
}

// waitForMCPReady blocks until the MCP background goroutine sends its ready signal.
func waitForMCPReady(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// waitForAgentMsg waits for messages from the agent channel
func waitForAgentMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// waitForPermissionRequest waits for permission requests
func waitForPermissionRequest(ch chan *permissions.PermissionRequest) tea.Cmd {
	return func() tea.Msg {
		return permissionRequestMsg{request: <-ch}
	}
}

// waitForPermissionMention waits for permission mentions
func waitForPermissionMention(ch chan *permissions.PermissionMention) tea.Cmd {
	return func() tea.Msg {
		return permissionMentionMsg{mention: <-ch}
	}
}

// startZulipBridge initializes and starts the Zulip bridge
func (m *Model) startZulipBridge() tea.Cmd {
	return func() tea.Msg {
		if m.zulipBridge == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := m.zulipBridge.Start(ctx); err != nil {
			return zulip.ConnectionStatusMsg{
				Connected: false,
				Error:     err,
			}
		}

		// Bridge started successfully, begin waiting for messages
		return waitForZulipMsg(m.zulipMsgChan)()
	}
}

// waitForZulipMsg waits for a message from the Zulip bridge
func waitForZulipMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
