package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/logging"

	"github.com/google/uuid"
)

// handleACPPing handles GET /acp/ping — always returns 200 {} (exempt from auth).
func (s *Server) handleACPPing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("{}"))
}

// handleACPListAgents handles GET /acp/agents.
// Returns an empty list when agentDB is nil (not a 503).
func (s *Server) handleACPListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := acpAgentsListResponse{Agents: []acpAgentManifest{}}

	if s.agentDB != nil {
		_, isAuthed := UserIDFromContext(r.Context())
		isService := IsServiceFromContext(r.Context())
		isAuthed = isAuthed || isService

		var agents []agentdb.Agent
		if isAuthed {
			internalAgents, _ := s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityInternal), false)
			externalAgents, _ := s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityExternal), true)
			agents = append(internalAgents, externalAgents...)
		} else {
			agents, _ = s.agentDB.ListAgents(r.Context(), string(agentdb.AgentVisibilityExternal), true)
		}

		for _, a := range agents {
			resp.Agents = append(resp.Agents, dbAgentToManifest(a))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleACPAgentByName handles GET /acp/agents/{name}.
func (s *Server) handleACPAgentByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/acp/agents/")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	if s.agentDB == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	a, err := s.agentDB.GetAgent(r.Context(), name)
	if err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dbAgentToManifest(*a))
}

// handleACPCreateRun handles POST /acp/runs.
func (s *Server) handleACPCreateRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req acpRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.AgentName == "" {
		http.Error(w, "agent_name is required", http.StatusBadRequest)
		return
	}

	// Look up the agent — must exist and be published.
	var dbAgent *agentdb.Agent
	if s.agentDB != nil {
		var err error
		dbAgent, err = s.agentDB.GetAgent(r.Context(), req.AgentName)
		if err != nil || dbAgent == nil {
			http.Error(w, "agent not found", http.StatusNotFound)
			return
		}
		if dbAgent.PublishedAt == nil {
			http.Error(w, "agent not published", http.StatusNotFound)
			return
		}
	} else {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	// Auth gate: internal agents require authentication.
	if dbAgent.Visibility == agentdb.AgentVisibilityInternal {
		_, ok := UserIDFromContext(r.Context())
		if !ok && !IsServiceFromContext(r.Context()) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// External agent: proxy the request to the registered URL.
	if dbAgent.ExternalURL != "" {
		s.proxyACPRun(w, r, *dbAgent, req)
		return
	}

	// Resolve or create session. Attaching to an existing session requires the
	// same ownership as any other session access.
	callerID, _ := UserIDFromContext(r.Context())
	var sess *Session
	if req.SessionID != "" {
		var allowed bool
		sess, allowed = s.acpSessionForCaller(r.Context(), req.SessionID)
		if !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if sess == nil {
		sessionID := uuid.New().String()
		userID := callerID
		sess = newSession(sessionID, dbAgent.Name, "acp", userID, "", dbAgent.AllowedTools)
		sess.tracer = app.SetupTracer(s.cfg, sessionID)
		sess.llmCtx = buildLLMContextFromDBAgent(s, dbAgent)
		if sess.llmCtx.SystemPrompt != "" {
			sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
		}
		sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)
		s.mu.Lock()
		s.sessions[sessionID] = sess
		s.mu.Unlock()
	}

	// Parse input from ACP messages.
	content := acpMessagesToContent(req.Messages)
	if content == "" {
		http.Error(w, "no input content", http.StatusBadRequest)
		return
	}

	// Create the run.
	runID := uuid.New().String()
	now := time.Now()
	ctx, cancel := context.WithCancel(r.Context())
	run := &ACPRun{
		ID:        runID,
		SessionID:   sess.id,
		AgentName:   req.AgentName,
		ownerUserID: callerID,
		Status:      ACPRunStatusCreated,
		CreatedAt: now,
		eventCh:   make(chan agentproto.Event, 64),
		cancel:    cancel,
	}

	s.acpRunsMu.Lock()
	s.acpRuns[runID] = run
	s.acpRunsMu.Unlock()

	mode := req.Mode
	if mode == "" {
		mode = "sync"
	}

	switch mode {
	case "async":
		run.mu.Lock()
		run.Status = ACPRunStatusRunning
		run.mu.Unlock()
		// Use a background context so the async run outlives the HTTP request.
		asyncCtx, asyncCancel := context.WithCancel(context.Background())
		run.mu.Lock()
		oldCancel := run.cancel
		run.cancel = asyncCancel
		run.mu.Unlock()
		if oldCancel != nil {
			oldCancel() // release the original request-scoped cancel
		}
		go s.runACPAsync(asyncCtx, run, sess, content)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(run.toResponse())

	case "stream":
		run.mu.Lock()
		run.Status = ACPRunStatusRunning
		run.mu.Unlock()
		s.runACPStream(ctx, w, run, sess, content)

	default: // "sync"
		run.mu.Lock()
		run.Status = ACPRunStatusRunning
		run.mu.Unlock()
		s.runACPSync(ctx, run, sess, content)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(run.toResponse())
	}
}

// runACPSync runs the agentic loop synchronously and populates run.Output.
func (s *Server) runACPSync(ctx context.Context, run *ACPRun, sess *Session, content string) {
	var output []acpMessage

	sendEvent := func(ev agentproto.Event) {
		if ev.Type == agentproto.EventTypeChunk && ev.Chunk != nil && ev.Chunk.Content != "" {
			output = append(output, acpMessage{
				Role:  "assistant",
				Parts: []acpContentPart{{ContentType: "text/plain", Content: ev.Chunk.Content}},
			})
		}
	}

	_, _, _, _, err := handleMessage(s, sess, content, nil, sendEvent)
	now := time.Now()

	run.mu.Lock()
	defer run.mu.Unlock()
	run.FinishedAt = &now
	if err != nil {
		run.Status = ACPRunStatusFailed
		run.Error = err.Error()
	} else {
		run.Status = ACPRunStatusCompleted
		run.Output = output
	}
}

// runACPAsync runs the agentic loop in a goroutine.
func (s *Server) runACPAsync(ctx context.Context, run *ACPRun, sess *Session, content string) {
	s.runACPSync(ctx, run, sess, content)
}

// runACPStream streams SSE events to the client while running the agentic loop.
// A keepalive comment (": keepalive") is written every 15 seconds to prevent
// proxies and load balancers from silently dropping idle SSE connections.
func (s *Server) runACPStream(ctx context.Context, w http.ResponseWriter, run *ACPRun, sess *Session, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, canFlush := w.(http.Flusher)

	writeSSE := func(eventName, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, data)
		if canFlush {
			flusher.Flush()
		}
	}

	var outputParts []acpMessage

	sendEvent := func(ev agentproto.Event) {
		eventName, data := translateEventToSSE(ev)
		if eventName == "" {
			return
		}
		writeSSE(eventName, data)
		// Accumulate output for run record.
		if ev.Type == agentproto.EventTypeChunk && ev.Chunk != nil && ev.Chunk.Content != "" {
			outputParts = append(outputParts, acpMessage{
				Role:  "assistant",
				Parts: []acpContentPart{{ContentType: "text/plain", Content: ev.Chunk.Content}},
			})
		}
	}

	// Run handleMessage in a goroutine; send keepalives on the main goroutine
	// until it finishes so that proxy connections do not time out on long turns.
	type result struct {
		err error
	}
	doneCh := make(chan result, 1)
	go func() {
		_, _, _, _, err := handleMessage(s, sess, content, nil, sendEvent)
		doneCh <- result{err: err}
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var res result
loop:
	for {
		select {
		case res = <-doneCh:
			break loop
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			if canFlush {
				flusher.Flush()
			}
		}
	}

	now := time.Now()

	run.mu.Lock()
	run.FinishedAt = &now
	if res.err != nil {
		run.Status = ACPRunStatusFailed
		run.Error = res.err.Error()
		run.mu.Unlock()
		errData, _ := json.Marshal(map[string]string{"error": res.err.Error()})
		writeSSE("run.failed", string(errData))
	} else {
		run.Status = ACPRunStatusCompleted
		run.Output = outputParts
		run.mu.Unlock()
		completedData, _ := json.Marshal(run.toResponse())
		writeSSE("run.completed", string(completedData))
	}
}

// translateEventToSSE maps an agentproto.Event to (eventName, jsonData) for SSE.
// Returns ("", "") when the event should not be forwarded.
func translateEventToSSE(ev agentproto.Event) (string, string) {
	switch ev.Type {
	case agentproto.EventTypeChunk:
		if ev.Chunk == nil || ev.Chunk.Content == "" {
			return "", ""
		}
		part := map[string]interface{}{
			"role": "assistant",
			"part": map[string]string{
				"content_type": "text/plain",
				"content":      ev.Chunk.Content,
			},
		}
		data, _ := json.Marshal(part)
		return "message.part", string(data)

	case agentproto.EventTypeToolInvoked:
		if ev.ToolInvoked == nil {
			return "", ""
		}
		part := map[string]interface{}{
			"type":       "tool_invoked",
			"tool_name":  ev.ToolInvoked.ToolName,
			"tool_use_id": ev.ToolInvoked.ToolUseID,
		}
		data, _ := json.Marshal(part)
		return "message.part", string(data)

	case agentproto.EventTypeToolResult:
		if ev.ToolResult == nil {
			return "", ""
		}
		part := map[string]interface{}{
			"type":       "tool_result",
			"tool_name":  ev.ToolResult.ToolName,
			"tool_use_id": ev.ToolResult.ToolUseID,
			"result":     ev.ToolResult.Result,
			"is_error":   ev.ToolResult.IsError,
		}
		data, _ := json.Marshal(part)
		return "message.part", string(data)

	case agentproto.EventTypeDone:
		data, _ := json.Marshal(map[string]string{"status": "completed"})
		return "message.completed", string(data)

	case agentproto.EventTypeError:
		if ev.Error != nil && ev.Error.Fatal {
			data, _ := json.Marshal(map[string]string{"error": ev.Error.Message})
			return "run.failed", string(data)
		}
		return "", ""

	default:
		return "", ""
	}
}

// acpSessionForCaller looks up an in-memory session a run wants to attach to.
// It returns (nil, true) when there is no such session (a new one is created)
// and (nil, false) when the session exists but belongs to someone else.
func (s *Server) acpSessionForCaller(ctx context.Context, sessionID string) (*Session, bool) {
	s.mu.RLock()
	sess := s.sessions[sessionID]
	s.mu.RUnlock()
	if sess == nil {
		return nil, true
	}
	sess.mu.Lock()
	owner := sess.userID
	sess.mu.Unlock()
	if !callerMayAccess(ctx, owner) {
		return nil, false
	}
	return sess, true
}

// handleACPRunSubroute dispatches GET /acp/runs/{id} and POST /acp/runs/{id}[/cancel].
func (s *Server) handleACPRunSubroute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/acp/runs/")
	parts := strings.SplitN(rest, "/", 2)
	runID := parts[0]
	if runID == "" {
		http.NotFound(w, r)
		return
	}

	s.acpRunsMu.RLock()
	run, ok := s.acpRuns[runID]
	s.acpRunsMu.RUnlock()
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if !callerMayAccess(r.Context(), run.ownerUserID) {
		// Same answer as a missing run, so run IDs can't be probed.
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	// Sub-action?
	if len(parts) == 2 {
		switch parts[1] {
		case "cancel":
			s.handleACPCancelRun(w, r, run)
		default:
			http.NotFound(w, r)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(run.toResponse())
	case http.MethodPost:
		s.handleACPResumeRun(w, r, run)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleACPResumeRun routes a perm_response into the run's session.
func (s *Server) handleACPResumeRun(w http.ResponseWriter, r *http.Request, run *ACPRun) {
	var req acpResumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	sess, ok := s.sessions[run.SessionID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	sess.mu.Lock()
	respCh, exists := sess.permPending[req.RequestID]
	if exists {
		delete(sess.permPending, req.RequestID)
	}
	sess.mu.Unlock()

	if !exists {
		http.Error(w, "permission request not found", http.StatusNotFound)
		return
	}

	select {
	case respCh <- req.Approved:
	default:
		logging.Warnf("agentd: ACP resume: perm response channel full for request %s", req.RequestID)
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleACPCancelRun cancels a running ACP run.
func (s *Server) handleACPCancelRun(w http.ResponseWriter, r *http.Request, run *ACPRun) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	run.mu.Lock()
	if run.Status == ACPRunStatusRunning || run.Status == ACPRunStatusAwaiting {
		run.Status = ACPRunStatusCancelled
		now := time.Now()
		run.FinishedAt = &now
		if run.cancel != nil {
			run.cancel()
		}
	}
	run.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// handleACPSession handles GET /acp/session/{id}.
func (s *Server) handleACPSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/acp/session/")
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	s.mu.RLock()
	sess, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	sess.mu.Lock()
	owner := sess.userID
	sess.mu.Unlock()
	if !callerMayAccess(r.Context(), owner) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	sess.mu.Lock()
	info := agentproto.SessionInfo{
		ID:           sess.id,
		UserID:       sess.userID,
		Persona:      sess.persona,
		Source:       sess.source,
		Title:        sess.title,
		CreatedAt:    sess.createdAt.UTC().Truncate(time.Second),
		MessageCount: len(sess.messages),
	}
	sess.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// proxyACPRun transparently forwards a POST /acp/runs request to an external agent URL.
// It handles sync, async, and stream modes because we are a transparent proxy — the
// Content-Type and body from the external agent flow straight through, including SSE streams.
func (s *Server) proxyACPRun(w http.ResponseWriter, r *http.Request, agent agentdb.Agent, req acpRunRequest) {
	target := strings.TrimRight(agent.ExternalURL, "/") + "/acp/runs"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	body, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "failed to encode proxy request", http.StatusInternalServerError)
		return
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "failed to build proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	if accept := r.Header.Get("Accept"); accept != "" {
		proxyReq.Header.Set("Accept", accept)
	}

	if key := externalAgentAPIKey(agent); key != "" {
		proxyReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := s.externalAgentHTTPClient(agent).Do(proxyReq)
	if err != nil {
		http.Error(w, "external agent unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// acpMessagesToContent joins all text/plain parts from all messages into a single string.
func acpMessagesToContent(messages []acpMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		for _, part := range msg.Parts {
			if part.ContentType == "text/plain" && part.Content != "" {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(part.Content)
			}
		}
	}
	return sb.String()
}

// dbAgentToManifest converts an agentdb.Agent to an acpAgentManifest.
func dbAgentToManifest(a agentdb.Agent) acpAgentManifest {
	return acpAgentManifest{
		Name:               a.Name,
		DisplayName:        a.DisplayName,
		Description:        a.Description,
		InputContentTypes:  a.InputContentTypes,
		OutputContentTypes: a.OutputContentTypes,
		Visibility:         string(a.Visibility),
		PublishedAt:        a.PublishedAt,
	}
}

