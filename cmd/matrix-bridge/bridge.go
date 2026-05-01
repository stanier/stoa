package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/matrix"
)

const (
	bridgePermissionTimeout = 60 * time.Second
	bridgeShutdownTimeout   = 15 * time.Second
)

// pendingPerm tracks a Matrix permission request awaiting a reaction.
type pendingPerm struct {
	requestID    string
	roomID       string
	eventID      string // Matrix event ID of the permission-request message
	originalMsg  *channels.Message
	responseChan chan bool
	requestedAt  time.Time
}

// sessionConn holds all per-conversation state for one agentd session.
type sessionConn struct {
	key       string
	sessionID string
	conn      agentproto.ClientConn

	lastActivity   time.Time
	lastActivityMu sync.Mutex

	currentMsg   *channels.Message
	currentMsgMu sync.RWMutex

	pendingPerms map[string]*pendingPerm
	permMu       sync.Mutex

	lastSnapshot []chat.Message
	snapshotMu   sync.Mutex

	// threadRootID is the Matrix event ID of the thread root for room sessions.
	// Empty for DM sessions.
	threadRootID string
	// roomID is the Matrix room ID for room/thread sessions.
	roomID string
}

func (s *sessionConn) touchActivity() {
	s.lastActivityMu.Lock()
	s.lastActivity = time.Now()
	s.lastActivityMu.Unlock()
}

func (s *sessionConn) getLastActivity() time.Time {
	s.lastActivityMu.Lock()
	defer s.lastActivityMu.Unlock()
	return s.lastActivity
}

const authStatusCacheTTL = 5 * time.Minute

type matrixAuthStatus struct {
	matrixUserID string
	authorized   bool
	roleName     string
	rolePersona  string
	roleTools    []string
	cachedAt     time.Time
}

// Bridge connects Matrix to agentd, sharding sessions by room (DM room per user).
type Bridge struct {
	cfg         *config.Config
	agentClient agentproto.Client
	channel     *channels.MatrixChannel
	stopChan    chan struct{}
	wg          sync.WaitGroup
	httpClient  *http.Client

	authdURL     string
	serviceToken string

	authStatusCache   map[string]*matrixAuthStatus
	authStatusCacheMu sync.Mutex

	sessions map[string]*sessionConn
	mu       sync.RWMutex
	idleTTL  time.Duration
	sessFile string

	// reactionIndex maps roomID+":"+eventID → conversationKey
	reactionIndex   map[string]string
	reactionIndexMu sync.Mutex
}

// NewBridge creates a Bridge.
func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.MatrixChannel) *Bridge {
	idleTTL := cfg.Matrix.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	return &Bridge{
		cfg:             cfg,
		agentClient:     agentClient,
		channel:         channel,
		stopChan:        make(chan struct{}),
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		authdURL:        strings.TrimRight(os.Getenv("AGENTD_AUTHD_URL"), "/"),
		serviceToken:    os.Getenv("AGENTD_API_KEY"),
		authStatusCache: make(map[string]*matrixAuthStatus),
		sessions:        make(map[string]*sessionConn),
		idleTTL:         idleTTL,
		sessFile:        cfg.Matrix.SessionFile,
		reactionIndex:   make(map[string]string),
	}
}

func recoverPanic(ctx string) {
	if r := recover(); r != nil {
		logging.Errorf("Panic recovered in %s: %v\n%s", ctx, r, debug.Stack())
	}
}

func (b *Bridge) spawnRecoverable(name string, fn func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer recoverPanic(name)
		fn()
	}()
}

// conversationKey returns the session routing key for a message.
// For DMs it is "dm:<matrix_user_id>".
// For room messages it is "thread:<thread_root_event_id>" where
// threadRootID is stored in msg.Topic (set by channels/matrix.go).
// For a new root message (no existing thread) the caller must pass the
// event ID directly; conversationKey is only used when msg.Topic is set.
func conversationKey(msg channels.Message) string {
	if msg.Type == "direct" {
		return "dm:" + msg.SenderID
	}
	// Room message — key on the thread root event ID (stored in Topic).
	return "thread:" + msg.Topic
}

// reactionKey returns the map key used in reactionIndex.
func reactionKey(roomID, eventID string) string {
	return roomID + ":" + eventID
}

// --- authd integration ---

func (b *Bridge) fetchAuthStatus(ctx context.Context, matrixUserID string) (userID string, authorized bool) {
	if matrixUserID == "" || b.authdURL == "" || b.serviceToken == "" {
		return "", false
	}

	b.authStatusCacheMu.Lock()
	if cached, ok := b.authStatusCache[matrixUserID]; ok && time.Since(cached.cachedAt) < authStatusCacheTTL {
		b.authStatusCacheMu.Unlock()
		return cached.matrixUserID, cached.authorized
	}
	b.authStatusCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/matrix/status?matrix_user_id="+matrixUserID, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+b.serviceToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		b.authStatusCacheMu.Lock()
		b.authStatusCache[matrixUserID] = &matrixAuthStatus{cachedAt: time.Now()}
		b.authStatusCacheMu.Unlock()
		return "", false
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", false
	}

	var result struct {
		UserID     string `json:"user_id"`
		Authorized bool   `json:"authorized"`
		Role       *struct {
			Name         string   `json:"name"`
			Persona      string   `json:"persona"`
			AllowedTools []string `json:"allowed_tools"`
		} `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false
	}

	entry := &matrixAuthStatus{
		matrixUserID: result.UserID,
		authorized:   result.Authorized,
		cachedAt:     time.Now(),
	}
	if result.Role != nil {
		entry.roleName    = result.Role.Name
		entry.rolePersona = result.Role.Persona
		entry.roleTools   = result.Role.AllowedTools
	}

	b.authStatusCacheMu.Lock()
	b.authStatusCache[matrixUserID] = entry
	b.authStatusCacheMu.Unlock()

	return result.UserID, result.Authorized
}

func (b *Bridge) resolveAccessPolicy(ctx context.Context, matrixUserID string) config.MatrixRolePolicy {
	if b.authdURL == "" || b.serviceToken == "" {
		return config.MatrixRolePolicy{Allow: true, Persona: b.cfg.Matrix.Persona}
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[matrixUserID]
	b.authStatusCacheMu.Unlock()
	if !ok || time.Since(cached.cachedAt) >= authStatusCacheTTL {
		b.fetchAuthStatus(ctx, matrixUserID)
		b.authStatusCacheMu.Lock()
		cached = b.authStatusCache[matrixUserID]
		b.authStatusCacheMu.Unlock()
	}

	if cached == nil || cached.matrixUserID == "" || !cached.authorized {
		return b.cfg.Matrix.AccessPolicy.Unauthenticated
	}

	if cached.roleName != "" {
		logging.Debugf("matrix-bridge: %s matched DB role %q", matrixUserID, cached.roleName)
		return config.MatrixRolePolicy{
			Allow:        true,
			Persona:      cached.rolePersona,
			AllowedTools: cached.roleTools,
		}
	}

	return config.MatrixRolePolicy{Allow: true, Persona: b.cfg.Matrix.Persona}
}

func (b *Bridge) resolveUserID(ctx context.Context, matrixUserID string) string {
	if matrixUserID == "" || b.authdURL == "" || b.serviceToken == "" {
		return ""
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[matrixUserID]
	b.authStatusCacheMu.Unlock()
	if ok && cached.matrixUserID != "" {
		return cached.matrixUserID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/matrix/resolve?matrix_user_id="+matrixUserID, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+b.serviceToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ""
	}
	var result struct {
		UserID string `json:"user_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result.UserID
}

// --- session management ---

func (b *Bridge) getOrCreateSession(ctx context.Context, key, persona, userID string, allowedTools []string) (*sessionConn, error) {
	b.mu.RLock()
	sess, ok := b.sessions[key]
	b.mu.RUnlock()
	if ok {
		sess.touchActivity()
		return sess, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if sess, ok = b.sessions[key]; ok {
		sess.touchActivity()
		return sess, nil
	}

	var sessionID string
	store, _ := loadSessionStore(b.sessFile)
	if existingID, found := store[key]; found {
		conn, err := b.agentClient.Connect(ctx, existingID)
		if err == nil {
			logging.Infof("matrix-bridge: reconnected to existing session %s for key %q", existingID[:8], key)
			sess = &sessionConn{
				key:          key,
				sessionID:    existingID,
				conn:         conn,
				lastActivity: time.Now(),
				pendingPerms: make(map[string]*pendingPerm),
			}
			b.sessions[key] = sess
			b.spawnRecoverable("eventHandler:"+key, func() { b.sessionEventHandler(sess) })
			return sess, nil
		}
		logging.Warnf("matrix-bridge: persisted session %s for key %q no longer valid (%v); creating new", existingID[:8], key, err)
	}

	var err error
	sessionID, err = b.agentClient.CreateSession(ctx, agentproto.SessionConfig{
		Persona:      persona,
		Source:       "matrix",
		UserID:       userID,
		AllowedTools: allowedTools,
	})
	if err != nil {
		return nil, fmt.Errorf("create agentd session for %q: %w", key, err)
	}
	conn, err := b.agentClient.Connect(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("connect to agentd session %s: %w", sessionID, err)
	}
	logging.Infof("matrix-bridge: new session %s for key %q (persona=%q)", sessionID[:8], key, persona)

	sess = &sessionConn{
		key:          key,
		sessionID:    sessionID,
		conn:         conn,
		lastActivity: time.Now(),
		pendingPerms: make(map[string]*pendingPerm),
	}
	b.sessions[key] = sess

	store[key] = sessionID
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("matrix-bridge: save session store: %v", err)
	}

	b.spawnRecoverable("eventHandler:"+key, func() { b.sessionEventHandler(sess) })
	return sess, nil
}

func (b *Bridge) removeSession(key string) {
	b.mu.Lock()
	sess, ok := b.sessions[key]
	if ok {
		delete(b.sessions, key)
	}
	b.mu.Unlock()

	if !ok || sess == nil {
		return
	}

	b.reactionIndexMu.Lock()
	sess.permMu.Lock()
	for _, p := range sess.pendingPerms {
		delete(b.reactionIndex, reactionKey(p.roomID, p.eventID))
	}
	sess.permMu.Unlock()
	b.reactionIndexMu.Unlock()
}

// --- lifecycle ---

func (b *Bridge) Start(_ context.Context) error {
	b.spawnRecoverable("messageHandler", func() { b.messageHandler() })
	b.spawnRecoverable("reactionHandler", func() { b.reactionHandler() })
	b.startEvictionLoop()
	return nil
}

func (b *Bridge) Stop() {
	close(b.stopChan)

	b.mu.Lock()
	for _, sess := range b.sessions {
		_ = sess.conn.Close()
	}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Info("matrix-bridge: stopped gracefully")
	case <-time.After(bridgeShutdownTimeout):
		logging.Warn("matrix-bridge: shutdown timed out")
	}
}

// --- message handler ---

func (b *Bridge) messageHandler() {
	commandPrefix := b.cfg.Matrix.CommandPrefix
	if commandPrefix == "" {
		commandPrefix = "!"
	}

	for {
		select {
		case <-b.stopChan:
			return

		case msg, ok := <-b.channel.Messages():
			if !ok {
				return
			}

			logging.Infof("matrix-bridge: received message from %s (len=%d)", msg.SenderID, len(msg.Content))

			// Determine the session key.
			// DMs: keyed by sender ID.
			// Room messages: keyed by thread root event ID.
			//   - If the message is already threaded (msg.Topic is set), use that root.
			//   - If it is a new root message (msg.Topic is empty), use the message's own
			//     event ID as the thread root, starting a new thread.
			var key string
			var threadRootID string // non-empty only for room sessions
			if msg.Type == "direct" {
				key = "dm:" + msg.SenderID
			} else {
				if msg.Topic != "" {
					// Already in a thread — route to existing session.
					threadRootID = msg.Topic
				} else {
					// New root message — start a new thread keyed on this event ID.
					threadRootID = msg.ID
				}
				key = "thread:" + threadRootID
			}
			trimmed := strings.TrimSpace(msg.Content)

			// Handle account commands before session creation.
			if strings.HasPrefix(trimmed, commandPrefix) {
				fields := strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))
				if len(fields) > 0 {
					cmd := strings.ToLower(fields[0])
					switch cmd {
					case "link":
						b.handleAuthCommand(context.Background(), msg)
						continue
					case "unlink":
						b.handleUnlinkCommand(context.Background(), msg)
						continue
					case "status", "whoami":
						b.handleStatusCommand(context.Background(), msg)
						continue
					}
				}
			}

			policy := b.resolveAccessPolicy(context.Background(), msg.SenderID)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.cfg.Matrix.Persona
			}

			agentUserID := b.resolveUserID(context.Background(), msg.SenderID)

			sess, err := b.getOrCreateSession(context.Background(), key, effectivePersona, agentUserID, policy.AllowedTools)
			if err != nil {
				logging.Errorf("matrix-bridge: get/create session for %q: %v", key, err)
				_ = b.channel.SendResponse(msg, "Error connecting to agent: "+err.Error(), err)
				continue
			}

			// Record thread/room context for non-DM sessions so replies can be threaded.
			if threadRootID != "" {
				sess.threadRootID = threadRootID
				sess.roomID = msg.Recipient
			}

			sess.currentMsgMu.Lock()
			msgCopy := msg
			sess.currentMsg = &msgCopy
			sess.currentMsgMu.Unlock()

			if strings.HasPrefix(trimmed, commandPrefix) {
				b.handleCommand(sess, msgCopy, strings.TrimPrefix(trimmed, commandPrefix), commandPrefix)
				continue
			}

			if err := sess.conn.Send(agentproto.ClientMessage{
				Type: agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{
					Content: trimmed,
				},
			}); err != nil {
				logging.Errorf("matrix-bridge: send to agentd session %s: %v", sess.sessionID[:8], err)
			}

		case status := <-b.channel.StatusUpdates():
			if status.Connected {
				logging.Info("matrix-bridge: Matrix connected")
			} else {
				logging.Errorf("matrix-bridge: Matrix disconnected: %v", status.Error)
			}
		}
	}
}

// --- auth commands ---

func (b *Bridge) handleAuthCommand(ctx context.Context, msg channels.Message) {
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	body, _ := json.Marshal(map[string]string{"matrix_user_id": msg.SenderID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.authdURL+"/link/matrix/start", bytes.NewReader(body))
	if err != nil {
		_ = b.channel.SendResponse(msg, "Error starting link flow.", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.serviceToken)

	resp, err := b.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		_ = b.channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	defer resp.Body.Close()

	var result struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.URL == "" {
		_ = b.channel.SendResponse(msg, "Error parsing auth server response.", nil)
		return
	}

	minutes := result.ExpiresIn / 60
	_ = b.channel.SendResponse(msg, fmt.Sprintf(
		"To link your Matrix account, visit this URL within %d minutes:\n\n%s\n\nAfter signing in you'll receive a confirmation here.",
		minutes, result.URL,
	), nil)

	deadline := time.Duration(result.ExpiresIn) * time.Second
	go b.pollForLink(msg, msg.SenderID, deadline)
}

func (b *Bridge) pollForLink(msg channels.Message, matrixUserID string, deadline time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	timer := time.NewTimer(deadline)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case <-ticker.C:
			userID := b.resolveUserID(context.Background(), matrixUserID)
			if userID == "" {
				continue
			}
			b.authStatusCacheMu.Lock()
			if e, ok := b.authStatusCache[matrixUserID]; ok {
				e.matrixUserID = userID
			}
			b.authStatusCacheMu.Unlock()

			_ = b.channel.SendResponse(msg,
				"✅ Your Matrix account has been linked! Future conversations will be associated with your account.", nil)
			return
		}
	}
}

func (b *Bridge) handleUnlinkCommand(ctx context.Context, msg channels.Message) {
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		b.authdURL+"/link/matrix/unlink?matrix_user_id="+msg.SenderID, nil)
	if err != nil {
		_ = b.channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.serviceToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		_ = b.channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		_ = b.channel.SendResponse(msg, "Failed to unlink account.", nil)
		return
	}

	b.authStatusCacheMu.Lock()
	delete(b.authStatusCache, msg.SenderID)
	b.authStatusCacheMu.Unlock()

	_ = b.channel.SendResponse(msg,
		"Your Matrix account has been unlinked. Future conversations will no longer be associated with your account.", nil)
}

func (b *Bridge) handleStatusCommand(ctx context.Context, msg channels.Message) {
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	userID := b.resolveUserID(ctx, msg.SenderID)
	if userID == "" {
		_ = b.channel.SendResponse(msg,
			"Your Matrix account is **not linked**.\nUse `!link` to link it.", nil)
		return
	}

	_ = b.channel.SendResponse(msg,
		"✅ Your Matrix account is **linked** to your account.", nil)
}

// --- local commands ---

func (b *Bridge) handleCommand(sess *sessionConn, msg channels.Message, raw, prefix string) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		_ = b.channel.SendResponse(msg, fmt.Sprintf("No command specified. Use `%shelp` for available commands.", prefix), nil)
		return
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "clear":
		snapshot, err := b.agentClient.GetMessages(context.Background(), sess.sessionID)
		if err != nil {
			logging.Warnf("matrix-bridge: get messages before clear: %v", err)
		}
		if err := b.agentClient.ClearSession(context.Background(), sess.sessionID); err != nil {
			logging.Errorf("matrix-bridge: clear session: %v", err)
			_ = b.channel.SendResponse(msg, "Error clearing conversation: "+err.Error(), nil)
		} else {
			sess.snapshotMu.Lock()
			sess.lastSnapshot = snapshot
			sess.snapshotMu.Unlock()
			if len(snapshot) > 0 {
				text := fmt.Sprintf("Conversation cleared. **Session ID**: `%s`. %d messages saved. Use `%sresume` to restore.", sess.sessionID, len(snapshot), prefix)
				_ = b.channel.SendResponse(msg, text, nil)
			}
		}

	case "compact":
		resp, err := b.agentClient.CompactSession(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("matrix-bridge: compact session: %v", err)
			_ = b.channel.SendResponse(msg, "Error compacting conversation: "+err.Error(), nil)
		} else {
			text := fmt.Sprintf("Conversation compacted.\n**Before**: %d messages, %d tokens\n**After**: %d messages, %d tokens\n**Saved**: %d tokens",
				resp.MessagesBefore, resp.TokensBefore,
				resp.MessagesAfter, resp.TokensAfter,
				resp.TokensBefore-resp.TokensAfter)
			_ = b.channel.SendResponse(msg, text, nil)
		}

	case "tokens":
		resp, err := b.agentClient.GetTokens(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("matrix-bridge: get tokens: %v", err)
			_ = b.channel.SendResponse(msg, "Error getting token counts: "+err.Error(), nil)
		} else {
			_ = b.channel.SendResponse(msg, agentproto.FormatTokensResponse(resp), nil)
		}

	case "resume":
		sess.snapshotMu.Lock()
		snapshot := sess.lastSnapshot
		sess.snapshotMu.Unlock()
		if len(snapshot) == 0 {
			_ = b.channel.SendResponse(msg, fmt.Sprintf("No saved conversation to resume. Use `%sclear` first.", prefix), nil)
			return
		}
		if err := b.agentClient.RestoreMessages(context.Background(), sess.sessionID, snapshot); err != nil {
			logging.Errorf("matrix-bridge: restore messages: %v", err)
			_ = b.channel.SendResponse(msg, "Error restoring conversation: "+err.Error(), nil)
		} else {
			_ = b.channel.SendResponse(msg, fmt.Sprintf("Conversation restored (%d messages).", len(snapshot)), nil)
		}

	case "model":
		providerType, providerModel := "", ""
		if dp := b.cfg.LLM.DefaultProvider(); dp != nil {
			providerType, providerModel = dp.Type, dp.Model
		}
		text := fmt.Sprintf("**Provider**: `%s`  **Model**: `%s`", providerType, providerModel)
		_ = b.channel.SendResponse(msg, text, nil)

	case "help":
		text := fmt.Sprintf(
			"**Available commands** (prefix: `%s`)\n\n"+
				"- `%sclear` — clear conversation history (saves a snapshot)\n"+
				"- `%sresume` — restore the last cleared conversation\n"+
				"- `%scompact` — compact conversation history\n"+
				"- `%stokens` — show token usage for current conversation\n"+
				"- `%smodel` — show current LLM provider and model\n"+
				"- `%slink` — link your Matrix account to the auth system\n"+
				"- `%sunlink` — remove your account link\n"+
				"- `%sstatus` — show your account link status\n"+
				"- `%shelp` — show this message",
			prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix)
		_ = b.channel.SendResponse(msg, text, nil)

	default:
		_ = b.channel.SendResponse(msg,
			fmt.Sprintf("Unknown command: `%s%s`. Use `%shelp` for available commands.", prefix, cmd, prefix), nil)
	}
}

// --- reaction handler ---

func (b *Bridge) reactionHandler() {
	reactionCh := b.channel.Reactions()
	for {
		select {
		case <-b.stopChan:
			return
		case reaction, ok := <-reactionCh:
			if !ok {
				return
			}
			b.handleReaction(reaction)
		}
	}
}

func (b *Bridge) handleReaction(reaction matrix.ReactionEvent) {
	emoji := reaction.Emoji
	if emoji != "✅" && emoji != "❌" {
		return
	}
	approved := emoji == "✅"

	logging.Debugf("matrix-bridge: reaction %q (approved=%v) on %s:%s", emoji, approved, reaction.RoomID, reaction.TargetEvent)

	rk := reactionKey(reaction.RoomID, reaction.TargetEvent)
	b.reactionIndexMu.Lock()
	key, found := b.reactionIndex[rk]
	b.reactionIndexMu.Unlock()
	if !found {
		return
	}

	b.mu.RLock()
	sess := b.sessions[key]
	b.mu.RUnlock()
	if sess == nil {
		return
	}

	sess.permMu.Lock()
	defer sess.permMu.Unlock()

	var matched *pendingPerm
	var matchedID string
	for id, p := range sess.pendingPerms {
		if p.roomID == reaction.RoomID && p.eventID == reaction.TargetEvent {
			matched = p
			matchedID = id
			break
		}
	}
	if matched == nil {
		return
	}

	select {
	case matched.responseChan <- approved:
		if approved {
			logging.Infof("matrix-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("matrix-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.pendingPerms, matchedID)
		b.reactionIndexMu.Lock()
		delete(b.reactionIndex, rk)
		b.reactionIndexMu.Unlock()
	default:
		logging.Debugf("matrix-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
	}
}

// --- session event handler ---

func (b *Bridge) sessionEventHandler(sess *sessionConn) {
	events := sess.conn.Events()
	var accumContent strings.Builder

	for {
		select {
		case <-b.stopChan:
			return
		case e, ok := <-events:
			if !ok {
				logging.Infof("matrix-bridge: agentd connection closed for session %s (key=%q)", sess.sessionID[:8], sess.key)
				b.removeSession(sess.key)
				return
			}
			b.handleEvent(sess, e, &accumContent)
		}
	}
}

func (b *Bridge) handleEvent(sess *sessionConn, e agentproto.Event, accumContent *strings.Builder) {
	sess.currentMsgMu.RLock()
	currentMsg := sess.currentMsg
	sess.currentMsgMu.RUnlock()

	switch e.Type {
	case agentproto.EventTypeChunk:
		if e.Chunk != nil {
			accumContent.WriteString(e.Chunk.Content)
		}

	case agentproto.EventTypeDone:
		content := accumContent.String()
		accumContent.Reset()

		var thinking string
		if e.Done != nil {
			if e.Done.FullContent != "" {
				content = e.Done.FullContent
			}
			thinking = e.Done.FullThinking
		}
		if content == "" {
			content = "(no response)"
		}
		if thinking != "" {
			content = fmt.Sprintf("```\n[Thinking]\n%s\n```\n\n%s",
				strings.TrimSpace(thinking), strings.TrimSpace(content))
		}

		if currentMsg != nil {
			// For room/thread sessions, reply within the thread.
			if sess.threadRootID != "" && sess.roomID != "" {
				if _, err := b.channel.SendThreadedMessage(sess.roomID, sess.threadRootID, content); err != nil {
					logging.Errorf("matrix-bridge: send threaded response: %v", err)
				}
			} else {
				if err := b.channel.SendResponse(*currentMsg, content, nil); err != nil {
					logging.Errorf("matrix-bridge: send response: %v", err)
				}
			}
		}

	case agentproto.EventTypeToolInvoked:
		if e.ToolInvoked == nil || e.ToolInvoked.RequiresPermission {
			return
		}
		if currentMsg != nil {
			_ = b.channel.AddReaction(*currentMsg, "🔧")
		}
		var paramsStr string
		if len(e.ToolInvoked.Params) > 0 {
			if pretty, err := json.MarshalIndent(json.RawMessage(e.ToolInvoked.Params), "", "  "); err == nil {
				paramsStr = string(pretty)
			} else {
				paramsStr = string(e.ToolInvoked.Params)
			}
		}
		plain := "Tool invoked: " + e.ToolInvoked.ToolName + "\n" + paramsStr
		html := "<strong>Tool invoked:</strong> <code>" + e.ToolInvoked.ToolName + "</code><br><pre><code>" + paramsStr + "</code></pre>"
		if currentMsg != nil {
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
		}

	case agentproto.EventTypeToolResult:
		// Informational; agentd handles internally.

	case agentproto.EventTypePermRequest:
		if e.PermRequest == nil {
			return
		}
		b.handlePermRequest(sess, e.PermRequest, currentMsg)

	case agentproto.EventTypePermMention:
		if e.PermMention == nil {
			return
		}
		plain := "Tool " + e.PermMention.ToolName + " ran automatically: " + e.PermMention.Description
		html := "<strong>Tool <code>" + e.PermMention.ToolName + "</code> ran automatically:</strong> " + e.PermMention.Description
		if currentMsg != nil {
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
		}

	case agentproto.EventTypeCompaction:
		if e.Compaction == nil {
			return
		}
		note := fmt.Sprintf("_Conversation history compacted (%d → %d messages)._",
			e.Compaction.MessagesBefore, e.Compaction.MessagesAfter)
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg, note, nil)
		}

	case agentproto.EventTypeError:
		if e.Error == nil {
			return
		}
		plain := "Error: " + e.Error.Message
		html := "<strong>Error:</strong> " + e.Error.Message
		if currentMsg != nil {
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
		}
		if e.Error.Fatal {
			logging.Errorf("matrix-bridge: fatal agentd error on session %s: %s", sess.sessionID[:8], e.Error.Message)
		}
	}
}

// --- permission request ---

func (b *Bridge) handlePermRequest(sess *sessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("matrix-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
		_ = sess.conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{
				RequestID: req.RequestID,
				Approved:  false,
			},
		})
	}

	if originalMsg == nil {
		autoDeny("no message context")
		return
	}

	var paramsJSON string
	if len(req.Params) > 0 {
		if pretty, err := json.MarshalIndent(json.RawMessage(req.Params), "", "  "); err == nil {
			paramsJSON = string(pretty)
		}
	}

	plain := fmt.Sprintf("Permission request: %s\n%s\nParameters:\n%s\nReact with ✅ to approve or ❌ to deny. Times out in 60 seconds.",
		req.ToolName, req.Description, paramsJSON)
	html := fmt.Sprintf(
		"<strong>Permission request:</strong> <code>%s</code><br><strong>Description:</strong> %s<br><strong>Parameters:</strong><br><pre><code>%s</code></pre>React with ✅ to approve or ❌ to deny. Times out in 60 seconds.",
		req.ToolName, req.Description, paramsJSON)

	eventID, err := b.channel.SendFormattedMessage(*originalMsg, plain, html)
	if err != nil {
		logging.Warnf("matrix-bridge: send permission request message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	raw, _ := originalMsg.Raw.(*matrix.MessageEvent)
	roomID := ""
	if raw != nil {
		roomID = raw.RoomID
	}

	if roomID != "" && eventID != "" {
		if err := b.channel.AddReactionByID(roomID, eventID, "✅"); err != nil {
			logging.Warnf("matrix-bridge: add check reaction: %v", err)
		}
		if err := b.channel.AddReactionByID(roomID, eventID, "❌"); err != nil {
			logging.Warnf("matrix-bridge: add x reaction: %v", err)
		}
	}

	responseChan := make(chan bool, 1)
	perm := &pendingPerm{
		requestID:    req.RequestID,
		roomID:       roomID,
		eventID:      eventID,
		originalMsg:  originalMsg,
		responseChan: responseChan,
		requestedAt:  time.Now(),
	}
	sess.permMu.Lock()
	sess.pendingPerms[req.RequestID] = perm
	sess.permMu.Unlock()

	rk := reactionKey(roomID, eventID)
	b.reactionIndexMu.Lock()
	b.reactionIndex[rk] = sess.key
	b.reactionIndexMu.Unlock()

	shortID := req.RequestID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	b.spawnRecoverable("permWaiter:"+shortID, func() {
		approvalTimeout := b.cfg.Permissions.ApprovalTimeout
		if approvalTimeout <= 0 {
			approvalTimeout = bridgePermissionTimeout
		}

		var approved bool
		select {
		case approved = <-responseChan:
			logging.Infof("matrix-bridge: permission %s resolved: approved=%v", shortID, approved)
		case <-time.After(approvalTimeout):
			logging.Warnf("matrix-bridge: permission %s timed out after %s; denying", shortID, approvalTimeout)
			approved = false
			sess.permMu.Lock()
			delete(sess.pendingPerms, req.RequestID)
			sess.permMu.Unlock()
			b.reactionIndexMu.Lock()
			delete(b.reactionIndex, rk)
			b.reactionIndexMu.Unlock()
		case <-b.stopChan:
			approved = false
			sess.permMu.Lock()
			delete(sess.pendingPerms, req.RequestID)
			sess.permMu.Unlock()
			b.reactionIndexMu.Lock()
			delete(b.reactionIndex, rk)
			b.reactionIndexMu.Unlock()
		}

		if err := sess.conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{
				RequestID: req.RequestID,
				Approved:  approved,
			},
		}); err != nil {
			logging.Errorf("matrix-bridge: send perm response: %v", err)
		}
	})
}

// --- eviction ---

func (b *Bridge) startEvictionLoop() {
	b.spawnRecoverable("evictionLoop", func() {
		interval := b.idleTTL / 4
		if interval < time.Minute {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-b.stopChan:
				return
			case <-ticker.C:
				b.evictIdleSessions()
			}
		}
	})
}

func (b *Bridge) evictIdleSessions() {
	cutoff := time.Now().Add(-b.idleTTL)

	b.mu.Lock()
	var evicted []string
	for key, sess := range b.sessions {
		if sess.getLastActivity().Before(cutoff) {
			_ = sess.conn.Close()
			delete(b.sessions, key)
			evicted = append(evicted, key)
		}
	}
	b.mu.Unlock()

	if len(evicted) == 0 {
		return
	}

	logging.Infof("matrix-bridge: evicted %d idle sessions: %v", len(evicted), evicted)

	store, _ := loadSessionStore(b.sessFile)
	for _, key := range evicted {
		delete(store, key)
	}
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("matrix-bridge: save session store after eviction: %v", err)
	}
}
