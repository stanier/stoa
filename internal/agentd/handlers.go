package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/compaction"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/mcp"
	"github.com/stanier/stoa/internal/permissions"
	"golang.org/x/net/websocket"

	"github.com/google/uuid"
)

// handleCreateSession handles POST /sessions.
// Body (optional JSON): { "persona": "coder" }
// Response: { "session_id": "<uuid>" }
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req agentproto.SessionConfig
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	sessionID := uuid.New().String()
	sess := newSession(sessionID, req.Persona, req.Source, req.UserID, req.LLMKey, req.AllowedTools)
	sess.toolsLocked = len(req.AllowedTools) > 0 && IsServiceFromContext(r.Context())
	sess.tracer = app.SetupTracer(s.cfg, sessionID)

	// Build the initial LLM context (system prompt + tools).
	sess.llmCtx = app.BuildLLMContext(
		s.cfg,
		s.skillRegistry,
		s.promptReg,
		s.personaReg,
		req.Persona,
		req.AllowedTools,
		s.agentSkills...,
	)

	// DB agent overlay: if the persona matches a published agent in the registry,
	// use its system_prompt and allowed_tools instead of the default persona resolution.
	if s.agentDB != nil && req.Persona != "" {
		if dbAgent, err := s.agentDB.GetAgent(r.Context(), req.Persona); err == nil && dbAgent.PublishedAt != nil {
			sess.llmCtx = buildLLMContextFromDBAgent(s, dbAgent)
		}
	}

	// Register list_tools as a per-session tool so the LLM can discover
	// available tools on demand rather than receiving the full list up front.
	sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)

	// Prepend the system message to the conversation history.
	if sess.llmCtx.SystemPrompt != "" {
		sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
	}

	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	// Persist to DB if configured.
	if s.agentDB != nil {
		if err := s.agentDB.CreateSession(r.Context(), sessionID, req.UserID, req.Persona, req.Source); err != nil {
			logging.Warnf("agentd: db: create session %s: %v", sessionID, err)
		}
	}

	logging.Infof("agentd: created session %s (persona=%q, source=%q, user=%q)", sessionID, req.Persona, req.Source, req.UserID)
	s.metrics.sessionsCreated.WithLabelValues(req.Source, req.Persona).Inc()
	s.metrics.activeSessions.WithLabelValues(req.Source).Inc()
	s.runPostCreate(r.Context(), sessionID, req.UserID, req.Source)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(agentproto.CreateSessionResponse{SessionID: sessionID})
}

// handleClearSession handles POST /sessions/:id/clear.
// It resets the conversation history while keeping the system prompt.
func (s *Server) handleClearSession(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess.mu.Lock()
	// Keep only the leading system message (contains the system prompt + tool list).
	var kept []chat.Message
	for _, msg := range sess.messages {
		if msg.Role == "system" {
			kept = append(kept, msg)
			break
		}
	}
	sess.messages = kept
	sess.persistedMsgCount = 0 // reset high-water mark; DB messages cleared below
	sess.mu.Unlock()

	// Delete persisted messages from DB so the history doesn't bleed back on restore.
	if s.agentDB != nil {
		if err := s.agentDB.DeleteMessages(r.Context(), sess.id); err != nil {
			logging.Warnf("agentd: db: delete messages for %s after clear: %v", sess.id, err)
		}
	}

	logging.Infof("agentd: session %s cleared", sess.id)

	s.persistSession(sess)
	userID, _ := UserIDFromContext(r.Context())
	s.runPostClear(r.Context(), sess.id, userID)

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSession handles DELETE /sessions/:id.
// Removes the session from memory and, if configured, from the database.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	// Ownership check before deletion.
	if !IsServiceFromContext(r.Context()) {
		requesterID, _ := UserIDFromContext(r.Context())
		s.mu.RLock()
		sess, exists := s.sessions[sessionID]
		s.mu.RUnlock()
		if exists && sess.userID != "" && requesterID != "" && sess.userID != requesterID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	s.mu.Lock()
	existing, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	if ok {
		existing.mu.Lock()
		src := existing.source
		existing.mu.Unlock()
		s.metrics.activeSessions.WithLabelValues(src).Dec()
	}

	// Remove from DB even if not in memory (may have been evicted).
	if s.agentDB != nil {
		if err := s.agentDB.DeleteSession(r.Context(), sessionID); err != nil {
			logging.Warnf("agentd: db: delete session %s: %v", sessionID, err)
		}
	}

	if !ok && s.agentDB == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	logging.Infof("agentd: deleted session %s", sessionID)
	userID, _ := UserIDFromContext(r.Context())
	s.runPostDelete(r.Context(), sessionID, userID)
	w.WriteHeader(http.StatusNoContent)
}

// handleListSessions handles GET /sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// Determine which user is asking. Service accounts see all sessions;
	// regular users only see their own.
	requesterID, _ := UserIDFromContext(r.Context())
	isService := IsServiceFromContext(r.Context())

	filterUserID := requesterID
	if isService {
		filterUserID = "" // empty = no filter
	}

	// When DB is configured, query it for the authoritative list (includes
	// message counts and last_active_at from all historical sessions).
	if s.agentDB != nil {
		q := r.URL.Query().Get("q")
		var (
			dbSessions []agentdb.Session
			err        error
		)
		if q != "" {
			dbSessions, err = s.agentDB.SearchSessions(r.Context(), filterUserID, q)
		} else {
			dbSessions, err = s.agentDB.ListSessions(r.Context(), filterUserID)
		}
		if err != nil {
			logging.Warnf("agentd: db: list sessions: %v", err)
		} else {
			infos := make([]agentproto.SessionInfo, 0, len(dbSessions))
			for _, ds := range dbSessions {
				// Merge in-memory title and message count if session is active.
				msgCount := 0
				title := ds.Title
				s.mu.RLock()
				if sess, ok := s.sessions[ds.ID]; ok {
					sess.mu.Lock()
					msgCount = nonSystemCount(sess.messages)
					if sess.title != "" {
						title = sess.title
					}
					sess.mu.Unlock()
				}
				s.mu.RUnlock()
				infos = append(infos, agentproto.SessionInfo{
					ID:           ds.ID,
					UserID:       ds.UserID,
					Persona:      ds.Persona,
					Source:       ds.Source,
					Title:        title,
					ProjectID:    ds.ProjectID,
					CreatedAt:    ds.CreatedAt.UTC().Truncate(time.Second),
					LastActiveAt: ds.LastActiveAt.UTC().Truncate(time.Second),
					MessageCount: msgCount,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(infos)
			return
		}
	}

	// Fallback: in-memory list, filtered by user.
	s.mu.RLock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		// Exclude sessions explicitly owned by a different user.
		if !isService && requesterID != "" && sess.userID != "" && sess.userID != requesterID {
			continue
		}
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	infos := make([]agentproto.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		sess.mu.Lock()
		infos = append(infos, agentproto.SessionInfo{
			ID:           sess.id,
			UserID:       sess.userID,
			Persona:      sess.persona,
			Source:       sess.source,
			Title:        sess.title,
			CreatedAt:    sess.createdAt.UTC().Truncate(time.Second),
			MessageCount: len(sess.messages),
		})
		sess.mu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(infos)
}

// handleCompactSession handles POST /sessions/:id/compact.
// It runs manual compaction on the session's conversation history using the configured strategy.
func (s *Server) handleCompactSession(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.cfg.Compaction.Enabled {
		http.Error(w, "compaction is not enabled", http.StatusBadRequest)
		return
	}

	// TODO: Replace with server-returned token counts once the LLM client propagates
	// usage statistics (input_tokens / output_tokens) from API responses.
	tokenCounter := compaction.NewCharEstimateCounter()

	sess.mu.Lock()
	msgsBefore := make([]chat.Message, len(sess.messages))
	copy(msgsBefore, sess.messages)
	sess.mu.Unlock()

	if len(msgsBefore) <= s.cfg.Compaction.KeepRecent {
		http.Error(w, fmt.Sprintf("too few messages to compact (have %d, need more than %d)",
			len(msgsBefore), s.cfg.Compaction.KeepRecent), http.StatusBadRequest)
		return
	}

	tokensBefore, _ := tokenCounter.CountMessages(msgsBefore)

	var strategy compaction.Strategy
	switch s.cfg.Compaction.Strategy {
	case "importance":
		strategy = compaction.NewImportanceBasedStrategy(s.cfg.Compaction.KeepRecent)
	default:
		strategy = compaction.NewSlidingWindowStrategy(s.cfg.Compaction.KeepRecent)
	}

	compactor := compaction.NewDefaultCompactor(
		s.llmClient,
		tokenCounter,
		strategy,
		s.cfg.Compaction.MaxTokens,
		s.cfg.Compaction.CompactThreshold,
	)

	newMessages, err := compactor.Compact(r.Context(), msgsBefore)
	if err != nil {
		http.Error(w, fmt.Sprintf("compaction failed: %v", err), http.StatusInternalServerError)
		return
	}
	newMessages = append(newMessages, chat.NewSummaryMessage("Conversation manually compacted"))

	tokensAfter, _ := tokenCounter.CountMessages(newMessages)

	sess.mu.Lock()
	sess.messages = newMessages
	sess.mu.Unlock()

	logging.Infof("agentd: session %s compacted (%d→%d messages, %d→%d tokens)",
		sess.id, len(msgsBefore), len(newMessages), tokensBefore, tokensAfter)
	s.metrics.compactionsTotal.WithLabelValues("manual").Inc()

	s.persistSession(sess)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentproto.CompactResponse{
		MessagesBefore: len(msgsBefore),
		MessagesAfter:  len(newMessages),
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
	})
}

// maybeAutoCompact checks whether the session has exceeded the compaction
// threshold and, if so, compacts it in-place. currentTokens should be the
// total (input+output) so it matches what is shown to users via !tokens.
// Falls back to a char estimate when currentTokens is 0. Safe to call with a
// nil sendEvent (skips the event).
func (s *Server) maybeAutoCompact(ctx context.Context, sess *Session, currentTokens int, sendEvent func(agentproto.Event)) {
	if !s.cfg.Compaction.Enabled {
		return
	}

	// Use real token count when available, otherwise char estimate.
	tokenCount := currentTokens
	if tokenCount == 0 {
		sess.mu.Lock()
		msgs := make([]chat.Message, len(sess.messages))
		copy(msgs, sess.messages)
		sess.mu.Unlock()
		tokenCount, _ = compaction.NewCharEstimateCounter().CountMessages(msgs)
	}

	if tokenCount <= s.cfg.Compaction.CompactThreshold {
		return
	}

	sess.mu.Lock()
	msgsBefore := make([]chat.Message, len(sess.messages))
	copy(msgsBefore, sess.messages)
	sess.mu.Unlock()

	if len(msgsBefore) <= s.cfg.Compaction.KeepRecent {
		return
	}

	logging.Infof("agentd: auto-compacting session %s (%d tokens > threshold %d)",
		sess.id, tokenCount, s.cfg.Compaction.CompactThreshold)
	s.metrics.compactionsTotal.WithLabelValues("auto").Inc()

	var strategy compaction.Strategy
	switch s.cfg.Compaction.Strategy {
	case "importance":
		strategy = compaction.NewImportanceBasedStrategy(s.cfg.Compaction.KeepRecent)
	default:
		strategy = compaction.NewSlidingWindowStrategy(s.cfg.Compaction.KeepRecent)
	}

	compactor := compaction.NewDefaultCompactor(
		s.llmClient,
		compaction.NewCharEstimateCounter(),
		strategy,
		s.cfg.Compaction.MaxTokens,
		s.cfg.Compaction.CompactThreshold,
	)

	newMessages, err := compactor.Compact(ctx, msgsBefore)
	if err != nil {
		logging.Errorf("agentd: auto-compaction failed for session %s: %v", sess.id, err)
		return
	}
	newMessages = append(newMessages, chat.NewSummaryMessage("Conversation automatically compacted."))

	// Estimate tokens in the compacted context so !tokens reflects the new size.
	// This is a char estimate; it will be replaced by real API counts on the next turn.
	estimatedTokens, _ := compaction.NewCharEstimateCounter().CountMessages(newMessages)

	sess.mu.Lock()
	sess.messages = newMessages
	sess.lastInputTokens = estimatedTokens
	sess.mu.Unlock()

	logging.Infof("agentd: session %s auto-compacted (%d→%d messages)",
		sess.id, len(msgsBefore), len(newMessages))

	if sendEvent != nil {
		sendEvent(agentproto.Event{Type: agentproto.EventTypeCompaction})
	}

	sess.mu.Lock()
	userID := sess.userID
	sess.mu.Unlock()
	s.runPostCompact(ctx, sess.id, userID, tokenCount, estimatedTokens)
}

// handleGetTokens handles GET /sessions/:id/tokens.
// It returns token counts for the session's current conversation history.
func (s *Server) handleGetTokens(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess.mu.Lock()
	msgs := make([]chat.Message, len(sess.messages))
	copy(msgs, sess.messages)
	realInput := sess.lastInputTokens
	realOutput := sess.totalOutputTokens
	sess.mu.Unlock()

	var total int
	byRole := make(map[string]int)

	if realInput > 0 || realOutput > 0 {
		// Real API total: lastInputTokens already includes system prompt + tool schemas.
		// Use char estimate for per-role breakdown (approximate proportions only).
		total = realInput + realOutput
		byRole["input"] = realInput
		byRole["output"] = realOutput
		tokenCounter := compaction.NewCharEstimateCounter()
		for _, msg := range msgs {
			count, _ := tokenCounter.Count(msg.Content)
			byRole[string(msg.Role)] += count
		}
	} else {
		// Fall back to char estimate before first turn completes.
		tokenCounter := compaction.NewCharEstimateCounter()
		total, _ = tokenCounter.CountMessages(msgs)
		for _, msg := range msgs {
			count, _ := tokenCounter.Count(msg.Content)
			byRole[string(msg.Role)] += count
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentproto.TokensResponse{
		TotalTokens:   total,
		TotalMessages: len(msgs),
		ByRole:        byRole,
	})
}

// handleGetMessages handles GET /sessions/:id/messages.
// It returns the conversation history for a session, excluding system messages.
func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess.mu.Lock()
	msgs := make([]chat.Message, 0, len(sess.messages))
	for _, m := range sess.messages {
		if m.Role != "system" {
			msgs = append(msgs, m)
		}
	}
	sess.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs)
}

// handleRestoreMessages handles POST /sessions/:id/restore.
// It replaces the session's non-system conversation history with the provided messages.
func (s *Server) handleRestoreMessages(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msgs []chat.Message
	if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
		// Drain the body so the connection can be reused.
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	sess.mu.Lock()
	// Keep only the leading system message, then append the restored messages.
	var kept []chat.Message
	for _, m := range sess.messages {
		if m.Role == "system" {
			kept = append(kept, m)
			break
		}
	}
	sess.messages = append(kept, msgs...)
	sess.mu.Unlock()

	logging.Infof("agentd: session %s restored (%d messages)", sess.id, len(msgs))
	w.WriteHeader(http.StatusNoContent)
}

// handleStream upgrades the HTTP connection to a WebSocket for session streaming.
// It uses golang.org/x/net/websocket which is already a project dependency.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, sess *Session) {
	token := bearerToken(r)
	wsHandler := websocket.Handler(func(ws *websocket.Conn) {
		s.serveWebSocket(ws, sess, token)
	})
	wsHandler.ServeHTTP(w, r)
}

// wsWriter wraps a websocket.Conn with a mutex so concurrent goroutines can
// safely call send. (x/net/websocket does not guarantee concurrent writes.)
type wsWriter struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// send serialises ev as JSON and writes it to the WebSocket.
func (w *wsWriter) send(ev agentproto.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		logging.Warnf("agentd: failed to marshal event: %v", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := websocket.Message.Send(w.ws, string(data)); err != nil {
		logging.Warnf("agentd: WS send error: %v", err)
	}
}

// serveWebSocket is the core WebSocket session handler.
//
// Goroutine layout:
//  1. tokenRecheck goroutine – periodically re-validates the Bearer token against
//     authd and closes the connection if the token has been revoked or expired.
//  2. permForwarder goroutine – drains sess.permRequestChan and sess.permMentionChan,
//     forwards events to the client via wsWriter.
//  3. read loop (current goroutine) – processes ClientMessage JSON from the client.
//     "send_message" messages trigger a new goroutine running the agentic loop.
//     "perm_response" messages are delivered to sess.permPending channels.
//
// A semaphore (agentSem) ensures at most one agentic loop runs per session at a time.
func (s *Server) serveWebSocket(ws *websocket.Conn, sess *Session, token string) {
	logging.Infof("agentd: WS connected for session %s", sess.id)
	s.metrics.activeWSConns.Inc()
	defer func() {
		logging.Infof("agentd: WS disconnected for session %s", sess.id)
		s.metrics.activeWSConns.Dec()
		ws.Close()
	}()

	// done is closed when the read loop exits (connection closes).
	done := make(chan struct{})
	defer close(done)

	// Periodically re-validate the token so that revoked sessions are closed
	// promptly rather than running until the WebSocket is torn down naturally.
	if s.authdClient != nil && token != "" {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if _, err := s.authdClient.Verify(context.Background(), token); err != nil {
						logging.Infof("agentd: WS token revoked or expired for session %s, closing connection", sess.id)
						ws.Close()
						return
					}
				}
			}
		}()
	}

	writer := &wsWriter{ws: ws}
	sendEvent := func(ev agentproto.Event) {
		if err := s.runPreSend(context.Background(), sess.id, &ev); err != nil {
			return
		}
		writer.send(ev)
	}

	// Wire per-session tool implementations (spawn_agent, message_agent, etc.).
	registerSubAgentTools(s, sess, sendEvent)

	// agentSem prevents overlapping agentic loop invocations for this session.
	agentSem := make(chan struct{}, 1)

	// agentResultForwarder: drains sub-agent results from sess.resultQueue, emits
	// an AgentCompleted event, injects the result as a user message, then re-runs
	// the parent agentic loop (acquiring agentSem to serialize with the read loop).
	go func() {
		for {
			select {
			case <-done:
				cancelAllSubAgents(sess)
				return
			case result, ok := <-sess.resultQueue:
				if !ok {
					return
				}
				// Emit observability event.
				sendEvent(agentproto.Event{
					Type: agentproto.EventTypeAgentCompleted,
					AgentCompleted: &agentproto.AgentCompletedEvent{
						AgentID: result.agentID,
						IsError: result.isError,
						Result:  result.content,
					},
				})
				// Acquire semaphore — block until parent loop is idle.
				select {
				case agentSem <- struct{}{}:
				case <-done:
					cancelAllSubAgents(sess)
					return
				}
				go func(r subAgentResult) {
					defer func() { <-agentSem }()
					label := "completed"
					if r.isError {
						label = "error"
					}
					sess.mu.Lock()
					sess.messages = append(sess.messages, chat.NewUserMessage(
						fmt.Sprintf("[Agent %s %s]: %s", r.agentID, label, r.content),
					))
					sess.mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), agenticLoopTimeout)
					defer cancel()
					finalContent, finalThinking, inputTokens, outputTokens, err := runAgenticLoop(ctx, s, sess, sendEvent)
					sess.mu.Lock()
					sess.totalOutputTokens += outputTokens
					sess.mu.Unlock()
					if err != nil {
						sendEvent(agentproto.Event{
							Type:  agentproto.EventTypeError,
							Error: &agentproto.ErrorEvent{Message: err.Error()},
						})
					}
					sendEvent(agentproto.Event{
						Type: agentproto.EventTypeDone,
						Done: &agentproto.DoneEvent{
							FullContent:  finalContent,
							FullThinking: finalThinking,
							InputTokens:  inputTokens,
							OutputTokens: outputTokens,
						},
					})
				}(result)
			}
		}
	}()

	// permForwarder: drains the global permission channels and forwards events to
	// this session's client. MCP tools are registered globally (shared skillRegistry)
	// and therefore send permission requests to s.permsSetup.RequestChan, not to
	// per-session channels. Reading the global channels here is correct for the
	// current single-session-per-connection model.
	//
	// TODO: for true multi-session concurrency, introduce per-session channel
	// wrapping so requests can be routed to the correct WS connection.
	var permReqChan <-chan *permissions.PermissionRequest
	var permMentionChan <-chan *permissions.PermissionMention
	if s.permsSetup != nil {
		permReqChan = s.permsSetup.RequestChan
		permMentionChan = s.permsSetup.MentionChan
	}

	go func() {
		for {
			select {
			case <-done:
				// Connection closed — deny any pending permissions to unblock agentic loops.
				sess.mu.Lock()
				for id, ch := range sess.permPending {
					select {
					case ch <- false:
					default:
					}
					delete(sess.permPending, id)
				}
				sess.mu.Unlock()
				return

			case req, ok := <-permReqChan:
				if !ok {
					return
				}
				// Register a response channel BEFORE notifying the client so that
				// a fast perm_response can't race ahead of registration.
				respCh := make(chan bool, 1)
				sess.mu.Lock()
				sess.permPending[req.ID] = respCh
				sess.mu.Unlock()

				logging.Infof("agentd: permission request for %q (rank=%d, session %s)", req.ToolName, req.Rank, sess.id)

				// Notify the client.
				var paramsRaw json.RawMessage
				if len(req.Parameters) > 0 {
					if b, err := json.Marshal(req.Parameters); err == nil {
						paramsRaw = b
					}
				}
				sendEvent(agentproto.Event{
					Type: agentproto.EventTypePermRequest,
					PermRequest: &agentproto.PermRequestEvent{
						RequestID:   req.ID,
						ToolName:    req.ToolName,
						Description: req.Description,
						Params:      paramsRaw,
						Rank:        int(req.Rank),
					},
				})

				// Wire the response: when the client sends perm_response, the read
				// loop writes approved/denied to respCh. Forward that decision to
				// the PermissionRequest's ResponseChan so the blocked tool continues.
				go func(id string, respChan chan permissions.PermissionResponse) {
					select {
					case approved, ok := <-respCh:
						if ok {
							select {
							case respChan <- permissions.PermissionResponse{Approved: approved}:
							default:
								logging.Warnf("agentd: permission response channel full, dropping response (approved=%v)", approved)
							}
						}
					case <-done:
						select {
						case respChan <- permissions.PermissionResponse{Approved: false}:
						default:
						}
					}
				}(req.ID, req.ResponseChan)

			case mention, ok := <-permMentionChan:
				if !ok {
					return
				}
				sendEvent(agentproto.Event{
					Type: agentproto.EventTypePermMention,
					PermMention: &agentproto.PermMentionEvent{
						ToolName:    mention.ToolName,
						Description: mention.Message,
					},
				})
			}
		}
	}()

	// Read loop: process frames from the client.
	for {
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			// Connection closed or read error.
			return
		}

		var msg agentproto.ClientMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			logging.Warnf("agentd: bad client message: %v", err)
			continue
		}

		switch msg.Type {
		case agentproto.MessageTypeSendMessage:
			if msg.SendMessage == nil || (msg.SendMessage.Content == "" && len(msg.SendMessage.ImageParts) == 0) {
				continue
			}
			content := msg.SendMessage.Content
			imageParts := make([]chat.ImagePart, len(msg.SendMessage.ImageParts))
			for i, ip := range msg.SendMessage.ImageParts {
				imageParts[i] = chat.ImagePart{URL: ip.URL, Detail: ip.Detail}
			}

			// Acquire the semaphore (non-blocking). Reject if previous loop is still running.
			select {
			case agentSem <- struct{}{}:
			default:
				sendEvent(agentproto.Event{
					Type:  agentproto.EventTypeError,
					Error: &agentproto.ErrorEvent{Message: "a message is already being processed; please wait"},
				})
				continue
			}

			// Run the agentic loop in a goroutine so the read loop can still
			// process "perm_response" messages while the loop is executing.
			go func() {
				defer func() { <-agentSem }()
				finalContent, finalThinking, inputTokens, outputTokens, err := handleMessage(s, sess, content, imageParts, sendEvent)
				sess.mu.Lock()
				sess.totalOutputTokens += outputTokens
				sess.mu.Unlock()
				if err != nil {
					sendEvent(agentproto.Event{
						Type:  agentproto.EventTypeError,
						Error: &agentproto.ErrorEvent{Message: err.Error()},
					})
				}
				sendEvent(agentproto.Event{
					Type: agentproto.EventTypeDone,
					Done: &agentproto.DoneEvent{
						FullContent:  finalContent,
						FullThinking: finalThinking,
						InputTokens:  inputTokens,
						OutputTokens: outputTokens,
					},
				})
			}()

		case agentproto.MessageTypePermResponse:
			if msg.PermResponse == nil {
				logging.Warnf("agentd: perm_response with nil payload")
				continue
			}
			sess.mu.Lock()
			respCh, ok := sess.permPending[msg.PermResponse.RequestID]
			if ok {
				delete(sess.permPending, msg.PermResponse.RequestID)
			}
			sess.mu.Unlock()

			if ok {
				// Non-blocking: the agentic loop may have already timed out.
				select {
				case respCh <- msg.PermResponse.Approved:
				default:
				}
			} else {
				logging.Warnf("agentd: perm_response for unknown request ID %s", msg.PermResponse.RequestID)
			}

		default:
			logging.Warnf("agentd: unknown client message type: %q", msg.Type)
		}
	}
}

// handleGetUsage handles GET /sessions/usage.
// Returns today's and this month's token usage plus budget limits for the authenticated user.
// When agentDB is nil or userID is empty, returns a graceful "not available" response.
func (s *Server) handleGetUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, _ := UserIDFromContext(r.Context())
	if s.agentDB == nil || userID == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "usage tracking not available"})
		return
	}

	summary, err := s.agentDB.GetUsageSummary(r.Context(), userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summary)
}

// handleGetModels handles GET /models.
// Returns all models available across all configured providers.
func (s *Server) handleGetModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type providerInfo struct {
		Name   string   `json:"name"`
		Models []string `json:"models"`
	}

	var providers []providerInfo
	if s.providerRegistry != nil {
		for _, name := range s.providerRegistry.ProviderNames() {
			var models []string
			for _, m := range s.providerRegistry.ListAllModels() {
				if m.Provider == name {
					models = append(models, m.ID)
				}
			}
			providers = append(providers, providerInfo{Name: name, Models: models})
		}
	}
	if providers == nil {
		providers = []providerInfo{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"providers": providers})
}

// handleSessionConfig handles GET and PATCH /sessions/:id/config.
// GET returns the current per-session LLM overrides.
// PATCH merges the provided fields into the session overrides.
func (s *Server) handleSessionConfig(w http.ResponseWriter, r *http.Request, sess *Session) {
	switch r.Method {
	case http.MethodGet:
		sess.mu.Lock()
		o := sess.overrides
		sess.mu.Unlock()
		if o == nil {
			o = &llm.Overrides{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(o)

	case http.MethodPatch:
		var patch llm.Overrides
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		sess.mu.Lock()
		if sess.overrides == nil {
			sess.overrides = &llm.Overrides{}
		}
		o := sess.overrides
		if patch.Temperature != nil {
			o.Temperature = patch.Temperature
		}
		if patch.TopP != nil {
			o.TopP = patch.TopP
		}
		if patch.TopK != nil {
			o.TopK = patch.TopK
		}
		if patch.MaxTokens != nil {
			o.MaxTokens = patch.MaxTokens
		}
		if patch.EnableThinking != nil {
			o.EnableThinking = patch.EnableThinking
		}
		if patch.ThinkingBudgetTokens != nil {
			o.ThinkingBudgetTokens = patch.ThinkingBudgetTokens
		}
		if patch.EnableOAIThinking != nil {
			o.EnableOAIThinking = patch.EnableOAIThinking
		}
		if patch.Provider != nil {
			o.Provider = patch.Provider
		}
		if patch.Model != nil {
			o.Model = patch.Model
		}
		sess.mu.Unlock()

		if s.agentDB != nil {
			if data, err := json.Marshal(o); err == nil {
				if err := s.agentDB.UpdateOverrides(r.Context(), sess.id, data); err != nil {
					logging.Warnf("agentd: db: update overrides for %s: %v", sess.id, err)
				}
			}
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListTools handles GET /tools.
// Returns the full catalogue of registered tools with name, description, and source.
// Source is "builtin" for built-in tools or "mcp_<servername>" for MCP tools.
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type toolInfo struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Source      string `json:"source"`
	}

	allTools := s.skillRegistry.ListTools()
	result := make([]toolInfo, 0, len(allTools))
	for _, t := range allTools {
		source := "builtin"
		if mcpTool, ok := t.(*mcp.MCPToolAdapter); ok {
			source = "mcp_" + mcpTool.ServerName()
		} else if permTool, ok := t.(*mcp.PermissionAwareMCPTool); ok {
			source = "mcp_" + permTool.ServerName()
		}
		result = append(result, toolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Source:      source,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// handleSessionTools handles GET and PATCH /sessions/:id/tools.
// GET returns the session's current allowed_tools filter.
// PATCH replaces the allowed_tools filter and rebuilds the session's LLM context.
func (s *Server) handleSessionTools(w http.ResponseWriter, r *http.Request, sess *Session) {
	switch r.Method {
	case http.MethodGet:
		sess.mu.Lock()
		allowed := sess.allowedTools
		sess.mu.Unlock()
		if allowed == nil {
			allowed = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"allowed_tools": allowed,
		})

	case http.MethodPatch:
		sess.mu.Lock()
		locked := sess.toolsLocked
		sess.mu.Unlock()
		if locked && !IsServiceFromContext(r.Context()) {
			http.Error(w, "forbidden: tool restrictions for this session were set by a service account", http.StatusForbidden)
			return
		}

		var patch struct {
			AllowedTools []string `json:"allowed_tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		sess.mu.Lock()
		sess.allowedTools = patch.AllowedTools
		sess.llmCtx = app.BuildLLMContext(
			s.cfg, s.skillRegistry, s.promptReg, s.personaReg,
			sess.persona, sess.allowedTools,
			s.agentSkills...,
		)
		// Refresh the per-session list_tools reference so it sees the new context.
		sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)
		sess.mu.Unlock()

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

