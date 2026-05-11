package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/agentskills"
	"github.com/stanier/stoa/internal/agents/personas"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/apikeydclient"
	"github.com/stanier/stoa/internal/authdclient"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/llm/cache"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/mcp"
	"github.com/stanier/stoa/internal/prompts"
	"github.com/stanier/stoa/internal/skills"
)

// Server is the agentd HTTP/WebSocket server.
// It owns shared resources (LLM client, registries, MCP manager, permission setup)
// that are re-used across all sessions.
type Server struct {
	cfg              *config.Config
	llmClient        llm.Client
	llmCache         *cache.MemoryCache // non-nil when response caching is enabled
	providerRegistry *llm.ProviderRegistry
	skillRegistry *skills.Registry
	promptReg     *prompts.Registry
	personaReg    *personas.Registry
	permsSetup    *app.PermissionsSetup // may be nil when permissions disabled
	mcpManager    *mcp.ServerManager   // may be nil if no MCP configured
	agentSkills   []*agentskills.SkillMetadata

	// Auth — authdClient is set when AGENTD_AUTHD_URL is configured (production).
	authdClient   *authdclient.Client
	apikeydClient *apikeydclient.Client // validates external ACP caller keys (stoa-infgw_ prefix)

	// loopHooks contains optional callbacks invoked during the agentic loop.
	loopHooks LoopHooks

	// compactionHooks contains optional callbacks invoked around compaction events.
	compactionHooks CompactionHooks

	// sessionHooks contains optional callbacks invoked at session lifecycle events.
	sessionHooks SessionHooks

	// eventHooks contains optional callbacks invoked before each outbound WebSocket event.
	eventHooks EventHooks

	// agentDB is optional Postgres persistence for sessions and messages.
	// When nil, falls back to filesystem (AGENTD_SESSION_DIR).
	agentDB *agentdb.DB

	metrics    *Metrics
	promReg    *prometheus.Registry
	metricsHandler http.Handler

	sessions map[string]*Session
	mu       sync.RWMutex

	// acpRuns tracks ACP runs keyed by run ID.
	acpRuns   map[string]*ACPRun
	acpRunsMu sync.RWMutex

	// httpClient is used for proxying ACP runs to external agents.
	httpClient *http.Client

	// stopCh is closed when the server is stopping; signals background goroutines.
	stopCh chan struct{}
}

// NewServer constructs a fully-initialised Server from the given config.
// MCP tool discovery starts asynchronously; any sessions created before it
// completes will have a reduced tool list that updates once discovery finishes.
func NewServer(cfg *config.Config) (*Server, error) {
	llmClient := app.SetupLLMClient(cfg)
	if llmClient == nil {
		return nil, fmt.Errorf("failed to create LLM client")
	}

	skillRegistry := app.SetupSkillRegistry(cfg, "/")
	promptReg := app.SetupPromptRegistry(cfg)
	personaReg := app.SetupPersonaRegistry()

	permsSetup := app.SetupPermissions(cfg)

	agentSkillsMeta := app.SetupAgentSkills(cfg)
	if len(agentSkillsMeta) > 0 {
		skillRegistry.RegisterTool(agentskills.NewLoadSkillTool(agentSkillsMeta))
		skillRegistry.RegisterTool(agentskills.NewLoadSkillFileTool(agentSkillsMeta))
		logging.Infof("agentd: registered load_skill and load_skill_file for %d skills", len(agentSkillsMeta))
	}

	// Build multi-provider registry with background model discovery.
	// Refresh interval: 30 minutes (0 = startup only, no repeating).
	// Kept for per-session provider override (overrides.Provider); fallback chain
	// and stats are now handled by inference-gateway.
	providerReg := app.SetupProviderRegistry(cfg, context.Background(), 30*time.Minute)

	// If a fallback chain is configured, wrap the default client.
	// Note: rate limiting and stats are now in inference-gateway. FallbackClient here
	// is only active when agentd is NOT routing through inference-gateway (e.g. direct
	// provider access in dev mode).
	if len(cfg.Gateway.FallbackChain) > 0 {
		fc := llm.NewFallbackClient(providerReg, cfg.Gateway.FallbackChain)
		if cfg.Gateway.DynamicRouting {
			fc = fc.WithDynamicRouting(true)
			logging.Infof("agentd: provider fallback chain (dynamic routing enabled): %v", cfg.Gateway.FallbackChain)
		} else {
			logging.Infof("agentd: provider fallback chain: %v", cfg.Gateway.FallbackChain)
		}
		llmClient = fc
	}

	// Optionally wrap with a response cache.
	var llmCache *cache.MemoryCache
	if cfg.Gateway.Cache.Enabled {
		if cfg.Gateway.Cache.RedisURL != "" {
			logging.Warnf("agentd: redis cache not yet implemented, using memory cache")
		}
		providerName := ""
		modelName := ""
		if dp := cfg.LLM.DefaultProvider(); dp != nil {
			providerName = dp.Name
			modelName = dp.Model
		}
		llmCache = cache.NewMemoryCache(context.Background(), cfg.Gateway.Cache.MaxEntries)
		llmClient = cache.NewCachingClient(llmClient, llmCache, cfg.Gateway.Cache.TTL, providerName, modelName)
		logging.Infof("agentd: response cache enabled (ttl=%s, max_entries=%d)", cfg.Gateway.Cache.TTL, cfg.Gateway.Cache.MaxEntries)
	}

	// Build a dedicated Prometheus registry so agentd metrics are isolated from
	// any global defaults that might bleed in from test harnesses or other packages.
	promReg := prometheus.NewRegistry()
	promReg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	metrics := NewMetrics(promReg, llmCache)

	s := &Server{
		cfg:              cfg,
		llmClient:        llmClient,
		llmCache:         llmCache,
		providerRegistry: providerReg,
		skillRegistry:    skillRegistry,
		promptReg:        promptReg,
		personaReg:       personaReg,
		permsSetup:       permsSetup,
		agentSkills:      agentSkillsMeta,
		sessions:         make(map[string]*Session),
		acpRuns:          make(map[string]*ACPRun),
		httpClient:       &http.Client{Timeout: 5 * time.Minute},
		stopCh:           make(chan struct{}),
		metrics:          metrics,
		promReg:          promReg,
		metricsHandler:   promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}),
	}

	// Background cleanup: remove completed/failed/cancelled runs older than 24h.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
			}
			cutoff := time.Now().Add(-24 * time.Hour)

			// Snapshot IDs under a short read lock, then evaluate each run.
			s.acpRunsMu.RLock()
			ids := make([]string, 0, len(s.acpRuns))
			for id := range s.acpRuns {
				ids = append(ids, id)
			}
			s.acpRunsMu.RUnlock()

			var toDelete []string
			for _, id := range ids {
				s.acpRunsMu.RLock()
				run, ok := s.acpRuns[id]
				s.acpRunsMu.RUnlock()
				if !ok {
					continue
				}
				run.mu.Lock()
				done := run.Status == ACPRunStatusCompleted ||
					run.Status == ACPRunStatusFailed ||
					run.Status == ACPRunStatusCancelled
				finished := run.FinishedAt != nil && run.FinishedAt.Before(cutoff)
				run.mu.Unlock()
				if done && finished {
					toDelete = append(toDelete, id)
				}
			}

			if len(toDelete) > 0 {
				s.acpRunsMu.Lock()
				for _, id := range toDelete {
					delete(s.acpRuns, id)
				}
				s.acpRunsMu.Unlock()
			}
		}
	}()

	// Session DB — optional Postgres persistence.
	if dsn := envOr("AGENTD_DATABASE_URL", ""); dsn != "" {
		adb, err := agentdb.Open(context.Background(), dsn)
		if err != nil {
			return nil, fmt.Errorf("agentd: session db: %w", err)
		}
		s.agentDB = adb
		logging.Infof("agentd: session persistence via Postgres")
	}

	// Seed agent registry from config personas (idempotent — ON CONFLICT DO NOTHING).
	if s.agentDB != nil {
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer seedCancel()
		allPersonas := personaReg.ListPersonas()
		if len(allPersonas) > 0 {
			n, err := s.agentDB.SeedAgentsFromPersonas(seedCtx, allPersonas)
			if err != nil {
				logging.Warnf("agentd: config persona seeding failed: %v", err)
			} else if n > 0 {
				logging.Infof("agentd: seeded %d agents from config personas", n)
			}
		}
	}

	// Auth setup — delegates to authd when AGENTD_AUTHD_URL is set.
	if authdURL := envOr("AGENTD_AUTHD_URL", ""); authdURL != "" {
		s.authdClient = authdclient.New(authdURL)
		logging.Infof("agentd: auth via authd at %s", authdURL)
	}

	// apikeyd — validates external ACP caller keys (stoa-infgw_ prefix).
	if apikeydURL := envOr("AGENTD_APIKEYD_URL", cfg.Agentd.APIKeydURL); apikeydURL != "" {
		s.apikeydClient = apikeydclient.New(apikeydURL)
		logging.Infof("agentd: external ACP key validation via apikeyd at %s", apikeydURL)
	}

	// Write squid.conf and wait for proxy readiness (no-op if not configured).
	initSquid(cfg)

	// Start async MCP connection / tool discovery.
	if cfg != nil && cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mcpOpts := app.MCPSetupOptions{
			ConnectTimeout: mcpConnectTimeout,
			OnComplete: func(count int) {
				logging.Infof("agentd: MCP tool discovery complete (%d tools)", count)
				// Rebuild LLM context for all existing sessions now that MCP tools
				// are registered. New sessions created after this point will pick
				// up the tools automatically via BuildLLMContext.
				s.mu.RLock()
				sessions := make([]*Session, 0, len(s.sessions))
				for _, sess := range s.sessions {
					sessions = append(sessions, sess)
				}
				s.mu.RUnlock()

				for _, sess := range sessions {
					sess.mu.Lock()
					sess.llmCtx = app.BuildLLMContext(cfg, skillRegistry, promptReg, personaReg, sess.persona, sess.allowedTools, s.agentSkills...)
					sess.mu.Unlock()
				}
			},
		}
		if permsSetup != nil {
			mcpOpts.PermChecker = permsSetup.Checker
			mcpOpts.AuditLogger = permsSetup.AuditLogger
			// Note: we do NOT use the global permsSetup channels here.
			// Per-session channels are wired at session creation time via
			// each session's permRequestChan / permMentionChan. The MCP tools
			// are wrapped per-session in buildSessionLLMContext when needed.
			// For simplicity, the global channels are used here as the initial
			// registration channels; session-specific routing is handled by the
			// WebSocket handlers that drain the per-session channels.
			mcpOpts.PermReqChan = permsSetup.RequestChan
			mcpOpts.PermMentionChan = permsSetup.MentionChan
		}
		s.mcpManager = app.SetupMCPManager(cfg, skillRegistry, mcpOpts)
	}

	if cfg.Agentd.SessionDir != "" {
		s.loadPersistedSessions()
	}

	return s, nil
}

// loadPersistedSessions restores sessions — from Postgres if configured,
// otherwise from the filesystem session directory.
func (s *Server) loadPersistedSessions() {
	if s.agentDB != nil {
		s.loadSessionsFromDB()
		return
	}
	if s.cfg.Agentd.SessionDir == "" {
		return
	}
	dir := s.cfg.Agentd.SessionDir
	persisted, err := loadAllSessions(dir)
	if err != nil {
		logging.Warnf("agentd: persist: load sessions from %s: %v", dir, err)
		return
	}

	count := 0
	for _, ps := range persisted {
		sess := newSession(ps.ID, ps.Persona, ps.Source, "", "", nil)
		sess.createdAt = ps.CreatedAt
		sess.lastInputTokens = ps.LastInputTokens
		sess.totalOutputTokens = ps.TotalOutputTokens
		sess.tracer = app.SetupTracer(s.cfg, ps.ID)

		// Rebuild a fresh system prompt from current config/tools.
		sess.llmCtx = app.BuildLLMContext(
			s.cfg,
			s.skillRegistry,
			s.promptReg,
			s.personaReg,
			ps.Persona,
			nil,
			s.agentSkills...,
		)

		// Prepend fresh system message, then restore conversation history.
		if sess.llmCtx.SystemPrompt != "" {
			sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
		}
		sess.messages = append(sess.messages, ps.Messages...)

		s.mu.Lock()
		s.sessions[ps.ID] = sess
		s.mu.Unlock()
		count++
	}

	if count > 0 {
		logging.Infof("agentd: restored %d session(s) from %s", count, dir)
	}
}

// Stop signals background goroutines to exit. Call before stopping the HTTP server.
func (s *Server) Stop() {
	close(s.stopCh)
}

// Router returns the HTTP handler for the agentd server.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions", s.handleSessions)
	mux.HandleFunc("/sessions/usage", s.handleGetUsage)
	mux.HandleFunc("/sessions/", s.handleSessionSubroute)
	mux.HandleFunc("/tools", s.handleListTools)
	mux.HandleFunc("/models", s.handleGetModels)
	mux.Handle("/metrics", s.metricsHandler)

	// Agent registry CRUD.
	mux.HandleFunc("/agents", s.handleAgents)
	mux.HandleFunc("/agents/", s.handleAgentsByName)

	// Project CRUD.
	mux.HandleFunc("/projects", s.handleProjects)
	mux.HandleFunc("/projects/", s.handleProjectsByID)

	// ACP surface.
	mux.HandleFunc("/acp/ping", s.handleACPPing)
	mux.HandleFunc("/acp/agents", s.handleACPListAgents)
	mux.HandleFunc("/acp/agents/", s.handleACPAgentByName)
	mux.HandleFunc("/acp/runs", s.handleACPCreateRun)
	mux.HandleFunc("/acp/runs/", s.handleACPRunSubroute)
	mux.HandleFunc("/acp/session/", s.handleACPSession)

	return mux
}

// AuthdClient returns the authd client, or nil when not configured.
func (s *Server) AuthdClient() *authdclient.Client { return s.authdClient }

// ApikeydClient returns the apikeyd client used to validate external ACP caller keys,
// or nil when apikeyd is not configured.
func (s *Server) ApikeydClient() *apikeydclient.Client { return s.apikeydClient }

// AgentDB returns the session database, or nil when not configured.
func (s *Server) AgentDB() *agentdb.DB { return s.agentDB }

// loadSessionsFromDB restores all sessions and their messages from Postgres.
func (s *Server) loadSessionsFromDB() {
	dbSessions, err := s.agentDB.ListSessions(context.Background(), "")
	if err != nil {
		logging.Warnf("agentd: db: load sessions: %v", err)
		return
	}

	count := 0
	for _, ds := range dbSessions {
		msgs, err := s.agentDB.GetMessages(context.Background(), ds.ID)
		if err != nil {
			logging.Warnf("agentd: db: load messages for session %s: %v", ds.ID, err)
			continue
		}

		sess := newSession(ds.ID, ds.Persona, ds.Source, ds.UserID, "", nil)
		sess.createdAt = ds.CreatedAt
		sess.lastInputTokens = ds.LastInputTokens
		sess.totalOutputTokens = ds.TotalOutputTokens
		sess.title = ds.Title
		sess.tracer = app.SetupTracer(s.cfg, ds.ID)
		sess.llmCtx = app.BuildLLMContext(s.cfg, s.skillRegistry, s.promptReg, s.personaReg, ds.Persona, nil, s.agentSkills...)
		if len(ds.Overrides) > 0 && string(ds.Overrides) != "{}" {
			var o llm.Overrides
			if err := json.Unmarshal(ds.Overrides, &o); err == nil {
				sess.overrides = &o
			}
		}

		if sess.llmCtx.SystemPrompt != "" {
			sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
		}
		sess.messages = append(sess.messages, msgs...)
		// All loaded messages are already persisted.
		sess.persistedMsgCount = nonSystemCount(sess.messages)

		s.mu.Lock()
		s.sessions[ds.ID] = sess
		s.mu.Unlock()
		count++
	}

	if count > 0 {
		logging.Infof("agentd: restored %d session(s) from Postgres", count)
	}
}

// loadSessionFromDB fetches a single session from Postgres and registers it in
// the in-memory map. Returns nil, nil when the session doesn't exist in the DB.
// Safe to call concurrently: uses a write lock to prevent double-registration.
func (s *Server) loadSessionFromDB(ctx context.Context, sessionID string) (*Session, error) {
	ds, err := s.agentDB.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, nil
	}

	msgs, err := s.agentDB.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	sess := newSession(ds.ID, ds.Persona, ds.Source, ds.UserID, "", nil)
	sess.createdAt = ds.CreatedAt
	sess.lastInputTokens = ds.LastInputTokens
	sess.totalOutputTokens = ds.TotalOutputTokens
	sess.title = ds.Title
	sess.tracer = app.SetupTracer(s.cfg, ds.ID)
	sess.llmCtx = app.BuildLLMContext(s.cfg, s.skillRegistry, s.promptReg, s.personaReg, ds.Persona, nil, s.agentSkills...)
	if len(ds.Overrides) > 0 && string(ds.Overrides) != "{}" {
		var o llm.Overrides
		if err := json.Unmarshal(ds.Overrides, &o); err == nil {
			sess.overrides = &o
		}
	}

	if sess.llmCtx.SystemPrompt != "" {
		sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
	}
	sess.messages = append(sess.messages, msgs...)
	sess.persistedMsgCount = nonSystemCount(sess.messages)

	// Write lock: a concurrent goroutine may have already recovered the same
	// session. Prefer the existing entry to avoid duplicating state.
	s.mu.Lock()
	if existing, ok := s.sessions[sessionID]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	logging.Infof("agentd: recovered session %s from Postgres (%d messages)", sessionID, len(msgs))
	return sess, nil
}

// nonSystemCount returns the number of non-system messages in a slice.
func nonSystemCount(msgs []chat.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role != "system" {
			n++
		}
	}
	return n
}

// handleSessions dispatches POST /sessions and GET /sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateSession(w, r)
	case http.MethodGet:
		s.handleListSessions(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSessionSubroute dispatches requests under /sessions/:id/...
func (s *Server) handleSessionSubroute(w http.ResponseWriter, r *http.Request) {
	// Path: /sessions/<id>/ws
	path := r.URL.Path // e.g. "/sessions/abc123/ws"
	const prefix = "/sessions/"
	if len(path) <= len(prefix) {
		http.NotFound(w, r)
		return
	}
	rest := path[len(prefix):] // "abc123/ws"

	// Find the next slash
	slash := -1
	for i, c := range rest {
		if c == '/' {
			slash = i
			break
		}
	}

	// DELETE or PATCH /sessions/:id (no trailing slash or suffix).
	if slash < 0 {
		switch r.Method {
		case http.MethodDelete:
			s.handleDeleteSession(w, r, rest)
		case http.MethodPatch:
			s.handlePatchSession(w, r, rest)
		default:
			http.NotFound(w, r)
		}
		return
	}

	sessionID := rest[:slash]
	suffix := rest[slash+1:] // "ws"

	s.mu.RLock()
	sess, ok := s.sessions[sessionID]
	s.mu.RUnlock()

	if !ok {
		if s.agentDB != nil {
			var err error
			sess, err = s.loadSessionFromDB(r.Context(), sessionID)
			if err != nil {
				logging.Errorf("agentd: session recovery for %s: %v", sessionID, err)
				http.Error(w, "session recovery failed", http.StatusInternalServerError)
				return
			}
		}
		if sess == nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
	}

	// Ownership check: non-service users may not access sessions owned by someone else.
	// Sessions with no userID (unowned/legacy) are accessible to any authenticated user.
	if !IsServiceFromContext(r.Context()) {
		requesterID, _ := UserIDFromContext(r.Context())
		if sess.userID != "" && requesterID != "" && sess.userID != requesterID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	switch suffix {
	case "ws":
		s.handleStream(w, r, sess)
	case "clear":
		s.handleClearSession(w, r, sess)
	case "compact":
		s.handleCompactSession(w, r, sess)
	case "tokens":
		s.handleGetTokens(w, r, sess)
	case "messages":
		s.handleGetMessages(w, r, sess)
	case "restore":
		s.handleRestoreMessages(w, r, sess)
	case "config":
		s.handleSessionConfig(w, r, sess)
	case "tools":
		s.handleSessionTools(w, r, sess)
	default:
		http.NotFound(w, r)
	}
}
