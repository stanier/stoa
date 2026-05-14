package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/compaction"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/permissions"
)

// LocalClient implements agentproto.Client using an in-process Server.
// No network round-trips: session creation, message routing, and event delivery
// all happen in-memory via Go channels.
type LocalClient struct {
	srv *Server
}

// NewLocalClient creates a LocalClient backed by the given Server.
func NewLocalClient(srv *Server) *LocalClient {
	return &LocalClient{srv: srv}
}

// CreateSession creates a session directly on the embedded server.
func (c *LocalClient) CreateSession(ctx context.Context, cfg agentproto.SessionConfig) (string, error) {
	if cfg.Source == "" {
		cfg.Source = "tui"
	}

	sessionID := uuid.New().String()
	sess := newSession(sessionID, cfg.Persona, cfg.Source, cfg.UserID, cfg.LLMKey, cfg.AllowedTools)
	sess.tracer = app.SetupTracer(c.srv.cfg, sessionID)
	sess.llmCtx = app.BuildLLMContext(
		c.srv.cfg,
		c.srv.skillRegistry,
		c.srv.promptReg,
		c.srv.personaReg,
		cfg.Persona,
		cfg.AllowedTools,
		c.srv.agentSkills...,
	)
	sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)
	if sess.llmCtx.SystemPrompt != "" {
		sess.messages = append(sess.messages, systemMessage(sess.llmCtx.SystemPrompt))
	}

	c.srv.mu.Lock()
	c.srv.sessions[sessionID] = sess
	c.srv.mu.Unlock()

	logging.Infof("agentd: local: created session %s (persona=%q)", sessionID, cfg.Persona)
	c.srv.metrics.sessionsCreated.WithLabelValues(cfg.Source, cfg.Persona).Inc()
	c.srv.metrics.activeSessions.WithLabelValues(cfg.Source).Inc()
	c.srv.runPostCreate(ctx, sessionID, cfg.UserID, cfg.Source)

	return sessionID, nil
}

// ListSessions returns the in-memory session list.
func (c *LocalClient) ListSessions(ctx context.Context) ([]agentproto.SessionInfo, error) {
	c.srv.mu.RLock()
	sessions := make([]*Session, 0, len(c.srv.sessions))
	for _, sess := range c.srv.sessions {
		sessions = append(sessions, sess)
	}
	c.srv.mu.RUnlock()

	infos := make([]agentproto.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		sess.mu.Lock()
		infos = append(infos, agentproto.SessionInfo{
			ID:           sess.id,
			Persona:      sess.persona,
			Source:       sess.source,
			Title:        sess.title,
			CreatedAt:    sess.createdAt.UTC().Truncate(time.Second),
			MessageCount: nonSystemCount(sess.messages),
		})
		sess.mu.Unlock()
	}
	return infos, nil
}

// ClearSession resets the conversation history for a session, keeping the system message.
func (c *LocalClient) ClearSession(ctx context.Context, sessionID string) error {
	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	var kept []chat.Message
	for _, m := range sess.messages {
		if m.Role == "system" {
			kept = append(kept, m)
			break
		}
	}
	sess.messages = kept
	sess.persistedMsgCount = 0
	sess.mu.Unlock()
	logging.Infof("agentd: local: session %s cleared", sessionID)
	return nil
}

// CompactSession runs manual compaction on the session's conversation history.
func (c *LocalClient) CompactSession(ctx context.Context, sessionID string) (agentproto.CompactResponse, error) {
	if !c.srv.cfg.Compaction.Enabled {
		return agentproto.CompactResponse{}, fmt.Errorf("compaction is not enabled")
	}

	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return agentproto.CompactResponse{}, err
	}

	tokenCounter := compaction.NewCharEstimateCounter()

	sess.mu.Lock()
	msgsBefore := make([]chat.Message, len(sess.messages))
	copy(msgsBefore, sess.messages)
	sess.mu.Unlock()

	if len(msgsBefore) <= c.srv.cfg.Compaction.KeepRecent {
		return agentproto.CompactResponse{}, fmt.Errorf("too few messages to compact (have %d, need more than %d)",
			len(msgsBefore), c.srv.cfg.Compaction.KeepRecent)
	}

	tokensBefore, _ := tokenCounter.CountMessages(msgsBefore)

	var strategy compaction.Strategy
	switch c.srv.cfg.Compaction.Strategy {
	case "importance":
		strategy = compaction.NewImportanceBasedStrategy(c.srv.cfg.Compaction.KeepRecent)
	default:
		strategy = compaction.NewSlidingWindowStrategy(c.srv.cfg.Compaction.KeepRecent)
	}

	compactor := compaction.NewDefaultCompactor(
		c.srv.llmClient,
		tokenCounter,
		strategy,
		c.srv.cfg.Compaction.MaxTokens,
		c.srv.cfg.Compaction.CompactThreshold,
	)

	newMessages, err := compactor.Compact(ctx, msgsBefore)
	if err != nil {
		return agentproto.CompactResponse{}, fmt.Errorf("compaction failed: %w", err)
	}
	newMessages = append(newMessages, chat.NewSummaryMessage("Conversation manually compacted"))
	tokensAfter, _ := tokenCounter.CountMessages(newMessages)

	sess.mu.Lock()
	sess.messages = newMessages
	sess.mu.Unlock()

	c.srv.persistSession(sess)

	return agentproto.CompactResponse{
		MessagesBefore: len(msgsBefore),
		MessagesAfter:  len(newMessages),
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
	}, nil
}

// GetTokens returns token counts for the session's current conversation history.
func (c *LocalClient) GetTokens(ctx context.Context, sessionID string) (agentproto.TokensResponse, error) {
	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return agentproto.TokensResponse{}, err
	}

	sess.mu.Lock()
	msgs := make([]chat.Message, len(sess.messages))
	copy(msgs, sess.messages)
	realInput := sess.lastInputTokens
	realOutput := sess.totalOutputTokens
	sess.mu.Unlock()

	tokenCounter := compaction.NewCharEstimateCounter()
	var total int
	byRole := make(map[string]int)

	if realInput > 0 || realOutput > 0 {
		total = realInput + realOutput
		byRole["input"] = realInput
		byRole["output"] = realOutput
		for _, m := range msgs {
			count, _ := tokenCounter.Count(m.Content)
			byRole[string(m.Role)] += count
		}
	} else {
		total, _ = tokenCounter.CountMessages(msgs)
		for _, m := range msgs {
			count, _ := tokenCounter.Count(m.Content)
			byRole[string(m.Role)] += count
		}
	}

	return agentproto.TokensResponse{
		TotalTokens:   total,
		TotalMessages: len(msgs),
		ByRole:        byRole,
	}, nil
}

// GetMessages returns the non-system conversation history for a session.
func (c *LocalClient) GetMessages(ctx context.Context, sessionID string) ([]chat.Message, error) {
	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	var msgs []chat.Message
	for _, m := range sess.messages {
		if m.Role != "system" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// RestoreMessages replaces the non-system conversation history with the provided messages.
func (c *LocalClient) RestoreMessages(ctx context.Context, sessionID string, messages []chat.Message) error {
	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	var sys []chat.Message
	for _, m := range sess.messages {
		if m.Role == "system" {
			sys = append(sys, m)
		}
	}
	sess.messages = append(sys, messages...)
	sess.persistedMsgCount = 0
	sess.mu.Unlock()
	c.srv.persistSession(sess)
	return nil
}

// Connect returns an in-process ClientConn for the given session.
// Starts the goroutines equivalent to serveWebSocket but using Go channels
// instead of network I/O.
func (c *LocalClient) Connect(ctx context.Context, sessionID string) (agentproto.ClientConn, error) {
	sess, err := c.lookupSession(sessionID)
	if err != nil {
		return nil, err
	}
	lc := &localConn{
		events: make(chan agentproto.Event, 64),
		msgs:   make(chan agentproto.ClientMessage, 16),
		done:   make(chan struct{}),
		sess:   sess,
		srv:    c.srv,
	}
	go lc.serve()
	return lc, nil
}

// Close is a no-op; LocalClient holds no persistent network resources.
func (c *LocalClient) Close() error { return nil }

// lookupSession retrieves a session by ID from the server's in-memory map.
func (c *LocalClient) lookupSession(sessionID string) (*Session, error) {
	c.srv.mu.RLock()
	sess, ok := c.srv.sessions[sessionID]
	c.srv.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("local: session %s not found", sessionID)
	}
	return sess, nil
}

// localConn implements agentproto.ClientConn over in-process channels.
// It mirrors the goroutine layout of serveWebSocket without network I/O.
type localConn struct {
	events chan agentproto.Event
	msgs   chan agentproto.ClientMessage
	done   chan struct{}
	sess   *Session
	srv    *Server
}

// Send routes a client message into the in-process connection.
func (l *localConn) Send(msg agentproto.ClientMessage) error {
	select {
	case l.msgs <- msg:
		return nil
	case <-l.done:
		return fmt.Errorf("local: connection closed")
	}
}

// Events returns the server→client event channel.
func (l *localConn) Events() <-chan agentproto.Event { return l.events }

// Close signals the serve goroutine to stop and drains any pending events.
func (l *localConn) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

// serve is the goroutine equivalent of serveWebSocket, using channels instead
// of WebSocket I/O. Goroutine layout mirrors the WebSocket version exactly:
//  1. agentResultForwarder — injects sub-agent results and re-runs the parent loop.
//  2. permForwarder — drains global permission channels and forwards to the client.
//  3. message read loop — processes ClientMessages from the TUI.
func (l *localConn) serve() {
	defer close(l.events)

	sendEvent := func(ev agentproto.Event) {
		if err := l.srv.runPreSend(context.Background(), l.sess.id, &ev); err != nil {
			return
		}
		select {
		case l.events <- ev:
		case <-l.done:
		}
	}

	// Wire per-session tools (spawn_agent, message_agent, etc.).
	registerSubAgentTools(l.srv, l.sess, sendEvent)

	// agentSem: at most one agentic loop running per session at a time.
	agentSem := make(chan struct{}, 1)

	// agentResultForwarder: drains sub-agent results from sess.resultQueue, emits
	// an AgentCompleted event, injects the result as a user message, then re-runs
	// the parent agentic loop (acquiring agentSem to serialise with the read loop).
	go func() {
		for {
			select {
			case <-l.done:
				cancelAllSubAgents(l.sess)
				return
			case result, ok := <-l.sess.resultQueue:
				if !ok {
					return
				}
				sendEvent(agentproto.Event{
					Type: agentproto.EventTypeAgentCompleted,
					AgentCompleted: &agentproto.AgentCompletedEvent{
						AgentID: result.agentID,
						IsError: result.isError,
						Result:  result.content,
					},
				})
				select {
				case agentSem <- struct{}{}:
				case <-l.done:
					cancelAllSubAgents(l.sess)
					return
				}
				go func(r subAgentResult) {
					defer func() { <-agentSem }()
					defer func() {
						if r := recover(); r != nil {
							logging.Errorf("agentd: local: panic in sub-agent result handler: %v", r)
							sendEvent(agentproto.Event{
								Type:  agentproto.EventTypeError,
								Error: &agentproto.ErrorEvent{Message: fmt.Sprintf("internal error: %v", r)},
							})
							sendEvent(agentproto.Event{Type: agentproto.EventTypeDone, Done: &agentproto.DoneEvent{}})
						}
					}()
					label := "completed"
					if r.isError {
						label = "error"
					}
					l.sess.mu.Lock()
					l.sess.messages = append(l.sess.messages, chat.NewUserMessage(
						fmt.Sprintf("[Agent %s %s]: %s", r.agentID, label, r.content),
					))
					l.sess.mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), agenticLoopTimeout)
					defer cancel()
					finalContent, finalThinking, inputTokens, outputTokens, err := runAgenticLoop(ctx, l.srv, l.sess, sendEvent)
					l.sess.mu.Lock()
					l.sess.totalOutputTokens += outputTokens
					l.sess.mu.Unlock()
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

	// permForwarder: drain global permission channels and forward to the TUI.
	// Mirrors the WS permForwarder goroutine in handlers.go verbatim.
	var permReqChan <-chan *permissions.PermissionRequest
	var permMentionChan <-chan *permissions.PermissionMention
	if l.srv.permsSetup != nil {
		permReqChan = l.srv.permsSetup.RequestChan
		permMentionChan = l.srv.permsSetup.MentionChan
	}

	go func() {
		for {
			select {
			case <-l.done:
				// Connection closing — deny any pending permission requests to
				// unblock blocked agentic loops.
				l.sess.mu.Lock()
				for id, ch := range l.sess.permPending {
					select {
					case ch <- false:
					default:
					}
					delete(l.sess.permPending, id)
				}
				l.sess.mu.Unlock()
				return

			case req, ok := <-permReqChan:
				if !ok {
					return
				}
				// Register the response channel BEFORE notifying the client so
				// that a fast perm_response cannot race ahead of registration.
				respCh := make(chan bool, 1)
				l.sess.mu.Lock()
				l.sess.permPending[req.ID] = respCh
				l.sess.mu.Unlock()

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

				go func(id string, respChan chan permissions.PermissionResponse) {
					select {
					case approved, ok := <-respCh:
						if ok {
							select {
							case respChan <- permissions.PermissionResponse{Approved: approved}:
							default:
								logging.Warnf("agentd: local: permission response channel full (approved=%v)", approved)
							}
						}
					case <-l.done:
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

	// Message read loop: process ClientMessages from the TUI.
	for {
		select {
		case <-l.done:
			return
		case msg, ok := <-l.msgs:
			if !ok {
				return
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

				select {
				case agentSem <- struct{}{}:
				default:
					sendEvent(agentproto.Event{
						Type:  agentproto.EventTypeError,
						Error: &agentproto.ErrorEvent{Message: "a message is already being processed; please wait"},
					})
					continue
				}

				go func() {
					defer func() { <-agentSem }()
					defer func() {
						if r := recover(); r != nil {
							logging.Errorf("agentd: local: panic in message handler: %v", r)
							sendEvent(agentproto.Event{
								Type:  agentproto.EventTypeError,
								Error: &agentproto.ErrorEvent{Message: fmt.Sprintf("internal error: %v", r)},
							})
							sendEvent(agentproto.Event{
								Type: agentproto.EventTypeDone,
								Done: &agentproto.DoneEvent{},
							})
						}
					}()
					logging.Infof("agentd: local: handling message (session %s)", l.sess.id)
					finalContent, finalThinking, inputTokens, outputTokens, err := handleMessage(l.srv, l.sess, content, imageParts, sendEvent)
					l.sess.mu.Lock()
					l.sess.totalOutputTokens += outputTokens
					l.sess.mu.Unlock()
					if err != nil {
						logging.Errorf("agentd: local: message error (session %s): %v", l.sess.id, err)
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
					logging.Warnf("agentd: local: perm_response with nil payload")
					continue
				}
				l.sess.mu.Lock()
				respCh, ok := l.sess.permPending[msg.PermResponse.RequestID]
				if ok {
					delete(l.sess.permPending, msg.PermResponse.RequestID)
				}
				l.sess.mu.Unlock()
				if ok {
					select {
					case respCh <- msg.PermResponse.Approved:
					default:
					}
				} else {
					logging.Warnf("agentd: local: perm_response for unknown request ID %s", msg.PermResponse.RequestID)
				}

			default:
				logging.Warnf("agentd: local: unknown client message type: %q", msg.Type)
			}
		}
	}
}
