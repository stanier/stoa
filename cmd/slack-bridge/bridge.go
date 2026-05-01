package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/slack"
)

// mentionRe strips Slack @-mention markup (<@UXXXXXXXX>) from message text.
var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>`)

func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

const (
	bridgePermissionTimeout = 60 * time.Second
	bridgeShutdownTimeout   = 15 * time.Second
)

// pendingPerm tracks a Slack permission request awaiting a reaction.
type pendingPerm struct {
	requestID   string
	channelID   string
	messageTS   string // Slack timestamp of the permission-request message
	originalMsg *channels.Message
	responseChan chan bool
	requestedAt time.Time
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

// slackAuthStatus caches the result of an authd /link/slack/status check.
type slackAuthStatus struct {
	userID      string
	authorized  bool
	roleName    string
	rolePersona string
	roleTools   []string
	cachedAt    time.Time
}

// Bridge connects Slack to agentd, sharding sessions by conversation context.
// DM conversations each get their own agentd session, keyed by Slack user ID.
type Bridge struct {
	cfg         *config.Config
	agentClient agentproto.Client
	channel     *channels.SlackChannel
	stopChan    chan struct{}
	wg          sync.WaitGroup
	httpClient  *http.Client

	authdURL     string
	serviceToken string

	// emailCache maps Slack user ID → email, populated lazily.
	emailCache   map[string]string
	emailCacheMu sync.Mutex

	// authStatusCache caches recent Slack sender auth status.
	authStatusCache   map[string]*slackAuthStatus
	authStatusCacheMu sync.Mutex

	// sessions maps conversationKey → sessionConn.
	sessions map[string]*sessionConn
	mu       sync.RWMutex
	idleTTL  time.Duration
	sessFile string

	// reactionIndex maps channelID+":"+messageTS → conversationKey for O(1) reaction lookup.
	reactionIndex   map[string]string
	reactionIndexMu sync.Mutex
}

// NewBridge creates a Bridge.
func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.SlackChannel) *Bridge {
	idleTTL := cfg.Slack.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	return &Bridge{
		cfg:             cfg,
		agentClient:     agentClient,
		channel:         channel,
		stopChan:        make(chan struct{}),
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		authdURL:        strings.TrimRight(getEnv("AGENTD_AUTHD_URL", ""), "/"),
		serviceToken:    getEnv("AGENTD_API_KEY", ""),
		emailCache:      make(map[string]string),
		authStatusCache: make(map[string]*slackAuthStatus),
		sessions:        make(map[string]*sessionConn),
		idleTTL:         idleTTL,
		sessFile:        cfg.Slack.SessionFile,
		reactionIndex:   make(map[string]string),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

// conversationKey returns a stable string key for a Slack message's conversation.
//
//	DM:                    "dm:<slack_user_id>"
//	Channel root message:  "channel:<channel_id>:thread:<message_ts>"
//	Channel threaded reply: "channel:<channel_id>:thread:<thread_ts>"
func conversationKey(msg channels.Message) string {
	if msg.Type == "direct" {
		return "dm:" + msg.SenderID
	}
	raw, _ := msg.Raw.(*slack.MessageEvent)
	if raw != nil && raw.ThreadTimestamp != "" {
		return "channel:" + raw.ChannelID + ":thread:" + raw.ThreadTimestamp
	}
	// Root channel message — key by the message's own timestamp to start a new thread.
	ts := msg.ID
	if raw != nil {
		ts = raw.Timestamp
	}
	return "channel:" + msg.Recipient + ":thread:" + ts
}

// reactionKey returns the map key used in reactionIndex.
func reactionKey(channelID, messageTS string) string {
	return channelID + ":" + messageTS
}

// --- authd integration ---

func (b *Bridge) fetchAuthStatus(ctx context.Context, email string) (userID string, authorized bool) {
	if email == "" || b.authdURL == "" || b.serviceToken == "" {
		return "", false
	}

	b.authStatusCacheMu.Lock()
	if cached, ok := b.authStatusCache[email]; ok && time.Since(cached.cachedAt) < authStatusCacheTTL {
		b.authStatusCacheMu.Unlock()
		return cached.userID, cached.authorized
	}
	b.authStatusCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/slack/status?email="+email, nil)
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
		b.authStatusCache[email] = &slackAuthStatus{cachedAt: time.Now()}
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

	entry := &slackAuthStatus{
		userID:     result.UserID,
		authorized: result.Authorized,
		cachedAt:   time.Now(),
	}
	if result.Role != nil {
		entry.roleName    = result.Role.Name
		entry.rolePersona = result.Role.Persona
		entry.roleTools   = result.Role.AllowedTools
	}

	b.authStatusCacheMu.Lock()
	b.authStatusCache[email] = entry
	b.authStatusCacheMu.Unlock()

	return result.UserID, result.Authorized
}

func (b *Bridge) resolveAccessPolicy(ctx context.Context, email string) config.SlackRolePolicy {
	if b.authdURL == "" || b.serviceToken == "" {
		return config.SlackRolePolicy{Allow: true, Persona: b.cfg.Slack.Persona}
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[email]
	b.authStatusCacheMu.Unlock()
	if !ok || time.Since(cached.cachedAt) >= authStatusCacheTTL {
		b.fetchAuthStatus(ctx, email)
		b.authStatusCacheMu.Lock()
		cached = b.authStatusCache[email]
		b.authStatusCacheMu.Unlock()
	}

	if cached == nil || cached.userID == "" || !cached.authorized {
		return b.cfg.Slack.AccessPolicy.Unauthenticated
	}

	if cached.roleName != "" {
		logging.Debugf("slack-bridge: %s matched DB role %q", email, cached.roleName)
		return config.SlackRolePolicy{
			Allow:        true,
			Persona:      cached.rolePersona,
			AllowedTools: cached.roleTools,
		}
	}

	return config.SlackRolePolicy{Allow: true, Persona: b.cfg.Slack.Persona}
}

func (b *Bridge) resolveUserID(ctx context.Context, email string) string {
	if email == "" || b.authdURL == "" || b.serviceToken == "" {
		return ""
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[email]
	b.authStatusCacheMu.Unlock()
	if ok && cached.userID != "" {
		return cached.userID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/slack/resolve?email="+email, nil)
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

// resolveEmail returns the email address for a Slack user ID, using the bridge
// email cache first, then falling back to the Slack API via the channel client.
func (b *Bridge) resolveEmail(ctx context.Context, slackUserID string) string {
	if slackUserID == "" {
		return ""
	}
	b.emailCacheMu.Lock()
	if email, ok := b.emailCache[slackUserID]; ok {
		b.emailCacheMu.Unlock()
		return email
	}
	b.emailCacheMu.Unlock()

	// SlackChannel wraps a *slack.Client; access email via the channel's embedded client.
	// We resolve lazily via a helper that the channel exposes indirectly through the API.
	// For now, reach through the type to the underlying Slack API client.
	email, err := b.channel.ResolveEmail(ctx, slackUserID)
	if err != nil {
		logging.Debugf("slack-bridge: resolve email for %s: %v", slackUserID, err)
		return ""
	}

	b.emailCacheMu.Lock()
	b.emailCache[slackUserID] = email
	b.emailCacheMu.Unlock()
	return email
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
			logging.Infof("slack-bridge: reconnected to existing session %s for key %q", existingID[:8], key)
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
		logging.Warnf("slack-bridge: persisted session %s for key %q no longer valid (%v); creating new", existingID[:8], key, err)
	}

	var err error
	sessionID, err = b.agentClient.CreateSession(ctx, agentproto.SessionConfig{
		Persona:      persona,
		Source:       "slack",
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
	logging.Infof("slack-bridge: new session %s for key %q (persona=%q)", sessionID[:8], key, persona)

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
		logging.Warnf("slack-bridge: save session store: %v", err)
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
		delete(b.reactionIndex, reactionKey(p.channelID, p.messageTS))
	}
	sess.permMu.Unlock()
	b.reactionIndexMu.Unlock()
}

// --- lifecycle ---

func (b *Bridge) Start(_ context.Context) error {
	b.spawnRecoverable("messageHandler", func() { b.messageHandler() })
	b.spawnRecoverable("reactionHandler", func() { b.reactionHandler() })
	b.spawnRecoverable("blockActionHandler", func() { b.blockActionHandler() })
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
		logging.Info("slack-bridge: stopped gracefully")
	case <-time.After(bridgeShutdownTimeout):
		logging.Warn("slack-bridge: shutdown timed out")
	}
}

// --- message handler ---

func (b *Bridge) messageHandler() {
	commandPrefix := b.cfg.Slack.CommandPrefix
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

			logging.Infof("slack-bridge: received message from %s (len=%d, type=%s)", msg.SenderID, len(msg.Content), msg.Type)

			// For channel messages, ensure the Raw.ThreadTimestamp is set so that
			// SendResponse routes replies into the thread.  For a root channel
			// message the ThreadTimestamp is empty at arrival; we use the message's
			// own Timestamp as the thread anchor so the bot reply starts a thread.
			if msg.Type == "channel" {
				if raw, ok := msg.Raw.(*slack.MessageEvent); ok && raw.ThreadTimestamp == "" {
					raw.ThreadTimestamp = raw.Timestamp
				}
			}

			key := conversationKey(msg)
			trimmed := strings.TrimSpace(msg.Content)

			// Resolve email lazily for authd integration.
			email := b.resolveEmail(context.Background(), msg.SenderID)

			// Handle account commands before session creation.
			if strings.HasPrefix(trimmed, commandPrefix) {
				fields := strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))
				if len(fields) > 0 {
					cmd := strings.ToLower(fields[0])
					switch cmd {
					case "link":
						b.handleAuthCommand(context.Background(), msg, email)
						continue
					case "unlink":
						b.handleUnlinkCommand(context.Background(), msg, email)
						continue
					case "status", "whoami":
						b.handleStatusCommand(context.Background(), msg, email)
						continue
					}
				}
			}

			// Resolve access policy.
			policy := b.resolveAccessPolicy(context.Background(), email)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.cfg.Slack.Persona
			}

			agentUserID := b.resolveUserID(context.Background(), email)

			sess, err := b.getOrCreateSession(context.Background(), key, effectivePersona, agentUserID, policy.AllowedTools)
			if err != nil {
				logging.Errorf("slack-bridge: get/create session for %q: %v", key, err)
				_ = b.channel.SendResponse(msg, "Error connecting to agent: "+err.Error(), err)
				continue
			}

			sess.currentMsgMu.Lock()
			msgCopy := msg
			sess.currentMsg = &msgCopy
			sess.currentMsgMu.Unlock()

			if strings.HasPrefix(trimmed, commandPrefix) {
				b.handleCommand(sess, msgCopy, strings.TrimPrefix(trimmed, commandPrefix), commandPrefix)
				continue
			}

			content := stripMentions(msg.Content)
			if err := sess.conn.Send(agentproto.ClientMessage{
				Type: agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{
					Content: content,
				},
			}); err != nil {
				logging.Errorf("slack-bridge: send to agentd session %s: %v", sess.sessionID[:8], err)
			}

		case status := <-b.channel.StatusUpdates():
			if status.Connected {
				logging.Info("slack-bridge: Slack connected")
			} else {
				logging.Errorf("slack-bridge: Slack disconnected: %v", status.Error)
			}
		}
	}
}

// --- auth commands ---

func (b *Bridge) handleAuthCommand(ctx context.Context, msg channels.Message, email string) {
	if msg.Type != "direct" {
		_ = b.channel.SendResponse(msg,
			"Account linking only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}
	if email == "" {
		_ = b.channel.SendResponse(msg,
			"Could not determine your Slack email address. Make sure the bot has the `users:read.email` scope.", nil)
		return
	}

	body, _ := json.Marshal(map[string]string{"slack_email": email})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.authdURL+"/link/slack/start", bytes.NewReader(body))
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
		"To link your Slack account, visit this URL within %d minutes:\n\n%s\n\n"+
			"After signing in you'll receive a confirmation here.",
		minutes, result.URL,
	), nil)

	deadline := time.Duration(result.ExpiresIn) * time.Second
	go b.pollForLink(msg, email, deadline)
}

func (b *Bridge) pollForLink(msg channels.Message, email string, deadline time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	timer := time.NewTimer(deadline)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case <-ticker.C:
			userID := b.resolveUserID(context.Background(), email)
			if userID == "" {
				continue
			}
			b.authStatusCacheMu.Lock()
			if e, ok := b.authStatusCache[email]; ok {
				e.userID = userID
			}
			b.authStatusCacheMu.Unlock()

			_ = b.channel.SendResponse(msg,
				"✅ Your Slack account has been linked! Future conversations will be associated with your account.", nil)
			return
		}
	}
}

func (b *Bridge) handleUnlinkCommand(ctx context.Context, msg channels.Message, email string) {
	if msg.Type != "direct" {
		_ = b.channel.SendResponse(msg,
			"Account unlinking only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" || email == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		b.authdURL+"/link/slack/unlink?email="+email, nil)
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

	b.emailCacheMu.Lock()
	delete(b.emailCache, msg.SenderID)
	b.emailCacheMu.Unlock()
	b.authStatusCacheMu.Lock()
	delete(b.authStatusCache, email)
	b.authStatusCacheMu.Unlock()

	_ = b.channel.SendResponse(msg,
		"Your Slack account has been unlinked. Future conversations will no longer be associated with your account.", nil)
}

func (b *Bridge) handleStatusCommand(ctx context.Context, msg channels.Message, email string) {
	if msg.Type != "direct" {
		_ = b.channel.SendResponse(msg,
			"Account status only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}
	if email == "" {
		_ = b.channel.SendResponse(msg,
			"Could not determine your Slack email address.", nil)
		return
	}

	userID := b.resolveUserID(ctx, email)
	if userID == "" {
		_ = b.channel.SendResponse(msg, fmt.Sprintf(
			"Your Slack account (%s) is *not linked*.\nUse `!link` to link it.", email,
		), nil)
		return
	}

	_ = b.channel.SendResponse(msg, fmt.Sprintf(
		"✅ Your Slack account (%s) is *linked* to your account.", email,
	), nil)
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
			logging.Warnf("slack-bridge: get messages before clear: %v", err)
		}
		if err := b.agentClient.ClearSession(context.Background(), sess.sessionID); err != nil {
			logging.Errorf("slack-bridge: clear session: %v", err)
			_ = b.channel.SendResponse(msg, "Error clearing conversation: "+err.Error(), nil)
		} else {
			sess.snapshotMu.Lock()
			sess.lastSnapshot = snapshot
			sess.snapshotMu.Unlock()
			if len(snapshot) > 0 {
				text := fmt.Sprintf("Conversation cleared. *Session ID*: `%s`. %d messages saved. Use `%sresume` to restore.", sess.sessionID, len(snapshot), prefix)
				_ = b.channel.SendResponse(msg, text, nil)
			}
		}

	case "compact":
		resp, err := b.agentClient.CompactSession(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("slack-bridge: compact session: %v", err)
			_ = b.channel.SendResponse(msg, "Error compacting conversation: "+err.Error(), nil)
		} else {
			text := fmt.Sprintf("Conversation compacted.\n*Before*: %d messages, %d tokens\n*After*: %d messages, %d tokens\n*Saved*: %d tokens",
				resp.MessagesBefore, resp.TokensBefore,
				resp.MessagesAfter, resp.TokensAfter,
				resp.TokensBefore-resp.TokensAfter)
			_ = b.channel.SendResponse(msg, text, nil)
		}

	case "tokens":
		resp, err := b.agentClient.GetTokens(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("slack-bridge: get tokens: %v", err)
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
			logging.Errorf("slack-bridge: restore messages: %v", err)
			_ = b.channel.SendResponse(msg, "Error restoring conversation: "+err.Error(), nil)
		} else {
			_ = b.channel.SendResponse(msg, fmt.Sprintf("Conversation restored (%d messages).", len(snapshot)), nil)
		}

	case "model":
		providerType, providerModel := "", ""
		if dp := b.cfg.LLM.DefaultProvider(); dp != nil {
			providerType, providerModel = dp.Type, dp.Model
		}
		text := fmt.Sprintf("*Provider*: `%s`  *Model*: `%s`", providerType, providerModel)
		_ = b.channel.SendResponse(msg, text, nil)

	case "help":
		text := fmt.Sprintf(
			"*Available commands* (prefix: `%s`)\n\n"+
				"• `%sclear` — clear conversation history (saves a snapshot)\n"+
				"• `%sresume` — restore the last cleared conversation\n"+
				"• `%scompact` — compact conversation history\n"+
				"• `%stokens` — show token usage for current conversation\n"+
				"• `%smodel` — show current LLM provider and model\n"+
				"• `%slink` — link your Slack account to the auth system (DM only)\n"+
				"• `%sunlink` — remove your account link (DM only)\n"+
				"• `%sstatus` — show your account link status (DM only)\n"+
				"• `%shelp` — show this message",
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

func (b *Bridge) handleReaction(reaction slack.ReactionEvent) {
	emoji := reaction.Emoji
	// Accept both standard names and Unicode aliases.
	if emoji != "white_check_mark" && emoji != "heavy_check_mark" && emoji != "x" {
		return
	}
	approved := emoji != "x"

	logging.Debugf("slack-bridge: reaction %q (approved=%v) on %s:%s", emoji, approved, reaction.ChannelID, reaction.MessageTS)

	rk := reactionKey(reaction.ChannelID, reaction.MessageTS)
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
		if p.channelID == reaction.ChannelID && p.messageTS == reaction.MessageTS {
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
			logging.Infof("slack-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("slack-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.pendingPerms, matchedID)
		b.reactionIndexMu.Lock()
		delete(b.reactionIndex, rk)
		b.reactionIndexMu.Unlock()
	default:
		logging.Debugf("slack-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
	}
}

// --- block action handler ---

func (b *Bridge) blockActionHandler() {
	blockActionCh := b.channel.BlockActions()
	for {
		select {
		case <-b.stopChan:
			return
		case action, ok := <-blockActionCh:
			if !ok {
				return
			}
			b.handleBlockAction(action)
		}
	}
}

func (b *Bridge) handleBlockAction(action slack.BlockActionEvent) {
	if action.Value != "approve" && action.Value != "deny" {
		return
	}
	approved := action.Value == "approve"

	logging.Debugf("slack-bridge: block action value=%q (approved=%v) on %s:%s", action.Value, approved, action.ChannelID, action.MessageTS)

	rk := reactionKey(action.ChannelID, action.MessageTS)
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
		if p.channelID == action.ChannelID && p.messageTS == action.MessageTS {
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
			logging.Infof("slack-bridge: permission %s approved via block action", matchedID[:8])
		} else {
			logging.Infof("slack-bridge: permission %s denied via block action", matchedID[:8])
		}
		delete(sess.pendingPerms, matchedID)
		b.reactionIndexMu.Lock()
		delete(b.reactionIndex, rk)
		b.reactionIndexMu.Unlock()
	default:
		logging.Debugf("slack-bridge: duplicate block action for permission %s, ignoring", matchedID[:8])
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
				logging.Infof("slack-bridge: agentd connection closed for session %s (key=%q)", sess.sessionID[:8], sess.key)
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
		// Wrap thinking in a Slack block quote when present.
		if thinking != "" {
			content = fmt.Sprintf("```\n[Thinking]\n%s\n```\n\n%s",
				strings.TrimSpace(thinking), strings.TrimSpace(content))
		}

		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, content, nil); err != nil {
				logging.Errorf("slack-bridge: send response: %v", err)
			}
		}

	case agentproto.EventTypeToolInvoked:
		if e.ToolInvoked == nil || e.ToolInvoked.RequiresPermission {
			return
		}
		if currentMsg != nil {
			_ = b.channel.AddReaction(*currentMsg, "wrench")
		}
		var paramsStr string
		if len(e.ToolInvoked.Params) > 0 {
			if pretty, err := json.MarshalIndent(json.RawMessage(e.ToolInvoked.Params), "", "  "); err == nil {
				paramsStr = string(pretty)
			} else {
				paramsStr = string(e.ToolInvoked.Params)
			}
		}
		detail := fmt.Sprintf("```\nTool invoked: %s\n%s\n```", e.ToolInvoked.ToolName, paramsStr)
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg, detail, nil)
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
		note := fmt.Sprintf("_Tool `%s` ran automatically: %s_", e.PermMention.ToolName, e.PermMention.Description)
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg, note, nil)
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
		errMsg := "Error: " + e.Error.Message
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg, errMsg, fmt.Errorf("%s", e.Error.Message))
		}
		if e.Error.Fatal {
			logging.Errorf("slack-bridge: fatal agentd error on session %s: %s", sess.sessionID[:8], e.Error.Message)
		}
	}
}

// --- permission request ---

func (b *Bridge) handlePermRequest(sess *sessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("slack-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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

	sectionText := fmt.Sprintf(
		"*Permission request*: `%s`\n\n*Description*: %s\n\n*Parameters*:\n```\n%s\n```",
		req.ToolName,
		req.Description,
		paramsJSON,
	)

	blocks := []map[string]any{
		{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": sectionText,
			},
		},
		{
			"type": "actions",
			"elements": []map[string]any{
				{
					"type":      "button",
					"action_id": "perm_approve",
					"value":     "approve",
					"text": map[string]any{
						"type": "plain_text",
						"text": "Approve",
					},
				},
				{
					"type":      "button",
					"action_id": "perm_deny",
					"value":     "deny",
					"text": map[string]any{
						"type": "plain_text",
						"text": "Deny",
					},
				},
			},
		},
	}

	msgTS, err := b.channel.SendBlocksMessageWithID(*originalMsg, blocks)
	if err != nil {
		logging.Warnf("slack-bridge: send permission block kit message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	// Get channel ID from the original message.
	raw, _ := originalMsg.Raw.(*slack.MessageEvent)
	channelID := ""
	if raw != nil {
		channelID = raw.ChannelID
	}

	responseChan := make(chan bool, 1)
	perm := &pendingPerm{
		requestID:    req.RequestID,
		channelID:    channelID,
		messageTS:    msgTS,
		originalMsg:  originalMsg,
		responseChan: responseChan,
		requestedAt:  time.Now(),
	}
	sess.permMu.Lock()
	sess.pendingPerms[req.RequestID] = perm
	sess.permMu.Unlock()

	rk := reactionKey(channelID, msgTS)
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
			logging.Infof("slack-bridge: permission %s resolved: approved=%v", shortID, approved)
		case <-time.After(approvalTimeout):
			logging.Warnf("slack-bridge: permission %s timed out after %s; denying", shortID, approvalTimeout)
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
			logging.Errorf("slack-bridge: send perm response: %v", err)
		}
	})
}

func (b *Bridge) addReactionWithRetry(channelID, messageTS, emoji string, maxAttempts int) error {
	const baseDelay = 300 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			time.Sleep(delay)
		}
		if err := b.channel.AddReactionByTS(channelID, messageTS, emoji); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
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

	logging.Infof("slack-bridge: evicted %d idle sessions: %v", len(evicted), evicted)

	store, _ := loadSessionStore(b.sessFile)
	for _, key := range evicted {
		delete(store, key)
	}
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("slack-bridge: save session store after eviction: %v", err)
	}
}
