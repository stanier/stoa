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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/zulip"
)

// mentionRe matches Zulip @-mention syntax in both forms:
//   - @**Full Name**  (user mention)
//   - @_**Full Name|12345**  (user mention with user ID)
//
// These are markup artifacts that add noise to the LLM inference payload.
var mentionRe = regexp.MustCompile(`@_?\*\*[^*]+\*\*`)

// stripMentions removes Zulip @-mention markup from a message before it is
// forwarded to the LLM. The plain text of the message is preserved; only the
// @**...** wrappers are removed.
func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

const (
	bridgePermissionTimeout = 60 * time.Second // Default timeout for permission approval
	bridgeShutdownTimeout   = 15 * time.Second // Graceful shutdown wait
)

// pendingPerm tracks a Zulip permission request awaiting a reaction.
type pendingPerm struct {
	requestID    string
	zulipMsgID   int
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

	// Current message context (the last message forwarded to agentd for this session)
	currentMsg   *channels.Message
	currentMsgMu sync.RWMutex

	// Permission handling — keyed by requestID
	pendingPerms map[string]*pendingPerm
	permMu       sync.Mutex

	// Snapshot of conversation history saved before the last !clear
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

// zulipAuthStatus caches the result of an authd /link/zulip/status check.
type zulipAuthStatus struct {
	userID       string
	authorized   bool
	// role fields — non-empty when the user has a DB-assigned role.
	roleName     string
	rolePersona  string
	roleTools    []string
	cachedAt     time.Time
}

// Bridge connects Zulip to agentd, sharding sessions by conversation context.
// Each unique (stream, topic) pair and each DM group gets its own agentd session.
type Bridge struct {
	cfg          *config.Config
	agentClient  agentproto.Client
	channel      *channels.ZulipChannel
	stopChan     chan struct{}
	wg           sync.WaitGroup
	httpClient   *http.Client

	// authd integration for Zulip account linking.
	// authdURL is read from AGENTD_AUTHD_URL; serviceToken from AGENTD_API_KEY.
	authdURL     string
	serviceToken string

	// userIDCache maps Zulip sender email → authd user_id.
	// Populated lazily; a resolved empty string means "not linked".
	userIDCache   map[string]string
	userIDCacheMu sync.Mutex

	// authStatusCache caches recent Zulip sender auth status to avoid hammering authd.
	authStatusCache   map[string]*zulipAuthStatus
	authStatusCacheMu sync.Mutex

	// Multi-session state: conversationKey → sessionConn
	sessions map[string]*sessionConn
	mu       sync.RWMutex
	idleTTL  time.Duration
	sessFile string

	// Global reaction lookup: zulipMsgID → conversationKey
	reactionIndex   map[int]string
	reactionIndexMu sync.Mutex
}

// NewBridge creates a Bridge.
func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.ZulipChannel) *Bridge {
	idleTTL := cfg.Zulip.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	sessFile := cfg.Zulip.SessionFile

	return &Bridge{
		cfg:              cfg,
		agentClient:      agentClient,
		channel:          channel,
		stopChan:         make(chan struct{}),
		httpClient:       &http.Client{Timeout: 5 * time.Second},
		authdURL:         strings.TrimRight(os.Getenv("AGENTD_AUTHD_URL"), "/"),
		serviceToken:     os.Getenv("AGENTD_API_KEY"),
		userIDCache:      make(map[string]string),
		authStatusCache:  make(map[string]*zulipAuthStatus),
		sessions:         make(map[string]*sessionConn),
		idleTTL:          idleTTL,
		sessFile:         sessFile,
		reactionIndex:    make(map[int]string),
	}
}

// recoverPanic logs panics from goroutines without crashing the process.
func recoverPanic(ctx string) {
	if r := recover(); r != nil {
		logging.Errorf("Panic recovered in %s: %v\n%s", ctx, r, debug.Stack())
	}
}

// spawnRecoverable starts fn in a tracked goroutine with panic recovery.
func (b *Bridge) spawnRecoverable(name string, fn func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer recoverPanic(name)
		fn()
	}()
}

// conversationKey maps a Zulip message to a stable string key that identifies
// its conversation context.
//
//	Stream message: "stream:<stream_name>:<topic>"
//	DM:             "dm:<sorted_comma_separated_user_ids>"
func conversationKey(msg channels.Message) string {
	if msg.Type == "stream" {
		return "stream:" + msg.Recipient + ":" + msg.Topic
	}
	// DM: extract and sort participant IDs from the raw Zulip message.
	if zm, ok := msg.Raw.(*zulip.Message); ok {
		ids := make([]string, 0, len(zm.DisplayRecipient.Recipients))
		for _, r := range zm.DisplayRecipient.Recipients {
			ids = append(ids, strconv.Itoa(r.ID))
		}
		sort.Strings(ids)
		return "dm:" + strings.Join(ids, ",")
	}
	// Fallback: sender-based key
	return "dm:" + msg.SenderID
}

// fetchAuthStatus calls authd's /link/zulip/status endpoint to check whether a
// Zulip email is linked and has an active session. Results are cached for authStatusCacheTTL.
// Returns (userID, authorized). userID is empty when the email is not linked.
func (b *Bridge) fetchAuthStatus(ctx context.Context, email string) (userID string, authorized bool) {
	if email == "" || b.authdURL == "" || b.serviceToken == "" {
		return "", false
	}

	// Check cache first.
	b.authStatusCacheMu.Lock()
	if cached, ok := b.authStatusCache[email]; ok && time.Since(cached.cachedAt) < authStatusCacheTTL {
		b.authStatusCacheMu.Unlock()
		return cached.userID, cached.authorized
	}
	b.authStatusCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/zulip/status?email="+email, nil)
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
		b.authStatusCache[email] = &zulipAuthStatus{userID: "", authorized: false, cachedAt: time.Now()}
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

	entry := &zulipAuthStatus{
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

// resolveAccessPolicy determines the ZulipRolePolicy for the given sender email.
// Authenticated users' roles are looked up from the authd database; the only
// config-driven policy is for unauthenticated senders.
func (b *Bridge) resolveAccessPolicy(ctx context.Context, email string) config.ZulipRolePolicy {
	// If authd is not configured, allow everything with the global persona.
	if b.authdURL == "" || b.serviceToken == "" {
		return config.ZulipRolePolicy{Allow: true, Persona: b.cfg.Zulip.Persona}
	}

	// Fetch (or use cached) status from authd.
	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[email]
	b.authStatusCacheMu.Unlock()
	if !ok || time.Since(cached.cachedAt) >= authStatusCacheTTL {
		b.fetchAuthStatus(ctx, email) // populates cache
		b.authStatusCacheMu.Lock()
		cached = b.authStatusCache[email]
		b.authStatusCacheMu.Unlock()
	}

	if cached == nil || cached.userID == "" || !cached.authorized {
		return b.cfg.Zulip.AccessPolicy.Unauthenticated
	}

	// Authenticated — apply DB role if one is assigned, otherwise use global defaults.
	if cached.roleName != "" {
		logging.Debugf("zulip-bridge: %s matched DB role %q", email, cached.roleName)
		return config.ZulipRolePolicy{
			Allow:        true,
			Persona:      cached.rolePersona,
			AllowedTools: cached.roleTools,
		}
	}

	// Authenticated, no role assigned — permissive default.
	return config.ZulipRolePolicy{Allow: true, Persona: b.cfg.Zulip.Persona}
}

// getOrCreateSession returns the sessionConn for the given key, creating and
// persisting a new agentd session if one does not already exist.
func (b *Bridge) getOrCreateSession(ctx context.Context, key, persona, userID string, allowedTools []string) (*sessionConn, error) {
	// Fast path: already have an active session.
	b.mu.RLock()
	sess, ok := b.sessions[key]
	b.mu.RUnlock()
	if ok {
		sess.touchActivity()
		return sess, nil
	}

	// Slow path: create or reconnect.
	b.mu.Lock()
	defer b.mu.Unlock()

	// Double-check after acquiring write lock.
	if sess, ok = b.sessions[key]; ok {
		sess.touchActivity()
		return sess, nil
	}

	// Try to reconnect to a persisted session.
	var sessionID string
	store, _ := loadSessionStore(b.sessFile)
	if existingID, found := store[key]; found {
		conn, err := b.agentClient.Connect(ctx, existingID)
		if err == nil {
			logging.Infof("zulip-bridge: reconnected to existing session %s for key %q", existingID[:8], key)
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
		logging.Warnf("zulip-bridge: persisted session %s for key %q no longer valid (%v); creating new", existingID[:8], key, err)
	}

	// Create a new agentd session.
	var err error
	sessionID, err = b.agentClient.CreateSession(ctx, agentproto.SessionConfig{
		Persona:      persona,
		Source:       "zulip",
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
	logging.Infof("zulip-bridge: new session %s for key %q (persona=%q)", sessionID[:8], key, persona)

	sess = &sessionConn{
		key:          key,
		sessionID:    sessionID,
		conn:         conn,
		lastActivity: time.Now(),
		pendingPerms: make(map[string]*pendingPerm),
	}
	b.sessions[key] = sess

	// Persist the new mapping.
	store[key] = sessionID
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("zulip-bridge: save session store: %v", err)
	}

	b.spawnRecoverable("eventHandler:"+key, func() { b.sessionEventHandler(sess) })
	return sess, nil
}

// removeSession removes a session from the in-memory map and cleans up its
// reactionIndex entries.
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

	// Clean up any reaction index entries for this session.
	b.reactionIndexMu.Lock()
	sess.permMu.Lock()
	for _, p := range sess.pendingPerms {
		delete(b.reactionIndex, p.zulipMsgID)
	}
	sess.permMu.Unlock()
	b.reactionIndexMu.Unlock()
}

// Start loads the persisted session store and begins the bridge goroutines.
func (b *Bridge) Start(ctx context.Context) error {
	b.spawnRecoverable("messageHandler", func() { b.messageHandler() })
	b.spawnRecoverable("reactionHandler", func() { b.reactionHandler() })
	b.startEvictionLoop()
	return nil
}

// Stop shuts down the bridge gracefully.
func (b *Bridge) Stop() {
	close(b.stopChan)

	// Close all active session connections.
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
		logging.Info("zulip-bridge: stopped gracefully")
	case <-time.After(bridgeShutdownTimeout):
		logging.Warn("zulip-bridge: shutdown timed out")
	}
}

// messageHandler reads from Zulip and forwards messages to the appropriate session.
func (b *Bridge) messageHandler() {
	commandPrefix := b.cfg.Zulip.CommandPrefix
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

			logging.Infof("zulip-bridge: received message from %s (len=%d)", msg.SenderName, len(msg.Content))

			key := conversationKey(msg)

			// Handle account commands before session creation (don't need a session).
			trimmed := strings.TrimSpace(msg.Content)
			if strings.HasPrefix(trimmed, commandPrefix) {
				cmd := strings.ToLower(strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))[0])
				if cmd == "link" {
					b.handleAuthCommand(context.Background(), msg)
					continue
				}
				if cmd == "unlink" {
					b.handleUnlinkCommand(context.Background(), msg)
					continue
				}
				if cmd == "status" || cmd == "whoami" {
					b.handleStatusCommand(context.Background(), msg)
					continue
				}
			}

			// Resolve access policy for this sender.
			policy := b.resolveAccessPolicy(context.Background(), msg.SenderEmail)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			// Determine effective persona (policy overrides global default).
			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.cfg.Zulip.Persona
			}

			// Resolve userID for session attribution.
			userID := b.resolveUserID(context.Background(), msg.SenderEmail)

			sess, err := b.getOrCreateSession(context.Background(), key, effectivePersona, userID, policy.AllowedTools)
			if err != nil {
				logging.Errorf("zulip-bridge: get/create session for %q: %v", key, err)
				_ = b.channel.SendResponse(msg, "Error connecting to agent: "+err.Error(), err)
				continue
			}

			// Store message context for this session.
			sess.currentMsgMu.Lock()
			msgCopy := msg
			sess.currentMsg = &msgCopy
			sess.currentMsgMu.Unlock()

			// Handle other commands locally.
			if strings.HasPrefix(trimmed, commandPrefix) {
				b.handleCommand(sess, msgCopy, strings.TrimPrefix(trimmed, commandPrefix), commandPrefix)
				continue
			}

			// Forward to agentd.
			content := stripMentions(msg.Content)
			if len(msg.Attachments) > 0 {
				var lines []string
				for _, a := range msg.Attachments {
					lines = append(lines, fmt.Sprintf("- %s (path_id: %s, size: %.1f KB)", a.Name, a.PathID, float64(a.Size)/1024))
				}
				content = "[Attachments — use mcp_zulip-mcp_zulip_download_attachment to fetch them]\n" +
					strings.Join(lines, "\n") + "\n\n" + content
			}
			if err := sess.conn.Send(agentproto.ClientMessage{
				Type: agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{
					Content: content,
				},
			}); err != nil {
				logging.Errorf("zulip-bridge: send to agentd session %s: %v", sess.sessionID[:8], err)
			}

		case status := <-b.channel.StatusUpdates():
			if status.Connected {
				logging.Infof("zulip-bridge: Zulip connected")
			} else {
				logging.Errorf("zulip-bridge: Zulip disconnected: %v", status.Error)
			}
		}
	}
}

// handleAuthCommand handles the !auth / !link command.
// Only works in direct messages. Calls authd to start a Zulip account link
// flow and replies with the URL, then polls for completion and notifies the user.
func (b *Bridge) handleAuthCommand(ctx context.Context, msg channels.Message) {
	if msg.Type != "private" {
		_ = b.channel.SendResponse(msg,
			"Account linking only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}
	if msg.SenderEmail == "" {
		_ = b.channel.SendResponse(msg,
			"Could not determine your Zulip email address.", nil)
		return
	}

	body, _ := json.Marshal(map[string]string{"zulip_email": msg.SenderEmail})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.authdURL+"/link/zulip/start", bytes.NewReader(body))
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
		"To link your Zulip account, visit this URL within %d minutes:\n\n%s\n\n"+
			"After signing in you'll receive a confirmation here.",
		minutes, result.URL,
	), nil)

	// Poll in the background and notify when the link completes.
	deadline := time.Duration(result.ExpiresIn) * time.Second
	go b.pollForLink(msg, msg.SenderEmail, deadline)
}

// pollForLink polls authd until the Zulip account link for email is resolved,
// then sends a confirmation DM. Stops after deadline elapses.
func (b *Bridge) pollForLink(msg channels.Message, email string, deadline time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	timer := time.NewTimer(deadline)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return // link expired; the browser page already told the user
		case <-ticker.C:
			userID := b.fetchUserID(context.Background(), email)
			if userID == "" {
				continue
			}
			// Update cache so the next session creation picks up the link.
			b.userIDCacheMu.Lock()
			b.userIDCache[email] = userID
			b.userIDCacheMu.Unlock()

			_ = b.channel.SendResponse(msg,
				"✅ Your Zulip account has been linked! "+
					"Future conversations will be associated with your account.", nil)
			return
		}
	}
}

// fetchUserID calls authd without using the cache — used by the polling goroutine.
func (b *Bridge) fetchUserID(ctx context.Context, email string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/zulip/resolve?email="+email, nil)
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

// handleUnlinkCommand handles !unlink — removes the Zulip account link.
// Only works in direct messages.
func (b *Bridge) handleUnlinkCommand(ctx context.Context, msg channels.Message) {
	if msg.Type != "private" {
		_ = b.channel.SendResponse(msg,
			"Account unlinking only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" || msg.SenderEmail == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		b.authdURL+"/link/zulip/unlink?email="+msg.SenderEmail, nil)
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

	// Clear the cache so future sessions won't carry the old user_id.
	b.userIDCacheMu.Lock()
	delete(b.userIDCache, msg.SenderEmail)
	b.userIDCacheMu.Unlock()

	_ = b.channel.SendResponse(msg,
		"Your Zulip account has been unlinked. Future conversations will no longer be associated with your account.", nil)
}

// handleStatusCommand handles !status / !whoami — shows account link state.
// Only works in direct messages.
func (b *Bridge) handleStatusCommand(ctx context.Context, msg channels.Message) {
	if msg.Type != "private" {
		_ = b.channel.SendResponse(msg,
			"Account status only works in a direct message with the bot.", nil)
		return
	}
	if b.authdURL == "" || b.serviceToken == "" || msg.SenderEmail == "" {
		_ = b.channel.SendResponse(msg,
			"Account linking is not configured on this server.", nil)
		return
	}

	userID := b.fetchUserID(ctx, msg.SenderEmail)
	if userID == "" {
		_ = b.channel.SendResponse(msg, fmt.Sprintf(
			"Your Zulip account (%s) is **not linked**.\n"+
				"Use `!auth` to link it.",
			msg.SenderEmail,
		), nil)
		return
	}

	// Update cache while we're here.
	b.userIDCacheMu.Lock()
	b.userIDCache[msg.SenderEmail] = userID
	b.userIDCacheMu.Unlock()

	_ = b.channel.SendResponse(msg, fmt.Sprintf(
		"✅ Your Zulip account (%s) is **linked** to your account.",
		msg.SenderEmail,
	), nil)
}

// resolveUserID returns the authd user_id for a Zulip sender email, or ""
// if the account is not linked. Results are cached for the lifetime of the bridge.
func (b *Bridge) resolveUserID(ctx context.Context, email string) string {
	if email == "" || b.authdURL == "" || b.serviceToken == "" {
		return ""
	}

	b.userIDCacheMu.Lock()
	if cached, ok := b.userIDCache[email]; ok {
		b.userIDCacheMu.Unlock()
		return cached
	}
	b.userIDCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/zulip/resolve?email="+email, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+b.serviceToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	userID := ""
	if resp.StatusCode == http.StatusOK {
		var result struct {
			UserID string `json:"user_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			userID = result.UserID
		}
	} else {
		// Drain body
		_, _ = io.Copy(io.Discard, resp.Body)
	}

	b.userIDCacheMu.Lock()
	b.userIDCache[email] = userID
	b.userIDCacheMu.Unlock()
	return userID
}

// handleCommand dispatches a local bridge command parsed from a message.
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
			logging.Warnf("zulip-bridge: get messages before clear: %v", err)
		}
		if err := b.agentClient.ClearSession(context.Background(), sess.sessionID); err != nil {
			logging.Errorf("zulip-bridge: clear session: %v", err)
			_ = b.channel.SendResponse(msg, "Error clearing conversation: "+err.Error(), nil)
		} else {
			sess.snapshotMu.Lock()
			sess.lastSnapshot = snapshot
			sess.snapshotMu.Unlock()
			if len(snapshot) > 0 {
				text := fmt.Sprintf("Conversation cleared.  **Session ID**: `%s`.  %d messages saved. Use `%sresume` to restore.", sess.sessionID, len(snapshot), prefix)
				logging.Infof("zulip-bridge: session %s cleared (snapshot: %d messages)", sess.sessionID[:8], len(snapshot))
				_ = b.channel.SendResponse(msg, text, nil)
			}
		}

	case "compact":
		resp, err := b.agentClient.CompactSession(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("zulip-bridge: compact session: %v", err)
			_ = b.channel.SendResponse(msg, "Error compacting conversation: "+err.Error(), nil)
		} else {
			logging.Infof("zulip-bridge: session %s compacted", sess.sessionID[:8])
			text := fmt.Sprintf("Conversation compacted.\n**Before**: %d messages, %d tokens\n**After**: %d messages, %d tokens\n**Saved**: %d tokens",
				resp.MessagesBefore, resp.TokensBefore,
				resp.MessagesAfter, resp.TokensAfter,
				resp.TokensBefore-resp.TokensAfter)
			_ = b.channel.SendResponse(msg, text, nil)
		}

	case "tokens":
		resp, err := b.agentClient.GetTokens(context.Background(), sess.sessionID)
		if err != nil {
			logging.Errorf("zulip-bridge: get tokens: %v", err)
			_ = b.channel.SendResponse(msg, "Error getting token counts: "+err.Error(), nil)
		} else {
			_ = b.channel.SendResponse(msg, agentproto.FormatTokensResponse(resp), nil)
		}

	case "resume":
		sess.snapshotMu.Lock()
		snapshot := sess.lastSnapshot
		sess.snapshotMu.Unlock()
		if len(snapshot) == 0 {
			_ = b.channel.SendResponse(msg, fmt.Sprintf("No saved conversation to resume. Use `%sclear` first to save a snapshot.", prefix), nil)
			return
		}
		if err := b.agentClient.RestoreMessages(context.Background(), sess.sessionID, snapshot); err != nil {
			logging.Errorf("zulip-bridge: restore messages: %v", err)
			_ = b.channel.SendResponse(msg, "Error restoring conversation: "+err.Error(), nil)
		} else {
			logging.Infof("zulip-bridge: session %s restored (%d messages)", sess.sessionID[:8], len(snapshot))
			_ = b.channel.SendResponse(msg, fmt.Sprintf("Conversation restored (%d messages).", len(snapshot)), nil)
		}

	case "model":
		text := fmt.Sprintf("**Provider**: `%s`  **Model**: `%s`", b.cfg.LLM.Provider, b.cfg.LLM.Model)
		_ = b.channel.SendResponse(msg, text, nil)

	case "help":
		text := fmt.Sprintf(
			"**Available commands** (prefix: `%s`)\n\n"+
				"- `%sclear` — clear conversation history (saves a snapshot)\n"+
				"- `%sresume` — restore the last cleared conversation\n"+
				"- `%scompact` — compact conversation history\n"+
				"- `%stokens` — show token usage for current conversation\n"+
				"- `%smodel` — show current LLM provider and model\n"+
				"- `%slink` — link your Zulip account to the auth system (DM only)\n"+
				"- `%sunlink` — remove your account link (DM only)\n"+
				"- `%sstatus` — show your account link status (DM only)\n"+
				"- `%shelp` — show this message",
			prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix, prefix)
		_ = b.channel.SendResponse(msg, text, nil)

	default:
		_ = b.channel.SendResponse(msg,
			fmt.Sprintf("Unknown command: `%s%s`. Use `%shelp` for available commands.", prefix, cmd, prefix), nil)
	}
}

// reactionHandler reads Zulip reactions and resolves pending permission requests.
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

// handleReaction resolves a pending permission using the reactionIndex for O(1) lookup.
func (b *Bridge) handleReaction(reaction zulip.ReactionReceivedMsg) {
	emojiName := reaction.EmojiName
	if emojiName != "check" && emojiName != "x" && emojiName != "cross_mark" {
		return
	}
	approved := emojiName == "check"

	logging.Debugf("zulip-bridge: reaction %q (approved=%v) on message %d", emojiName, approved, reaction.MessageID)

	// O(1) session lookup via index.
	b.reactionIndexMu.Lock()
	key, found := b.reactionIndex[reaction.MessageID]
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
		if p.zulipMsgID == reaction.MessageID {
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
			logging.Infof("zulip-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("zulip-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.pendingPerms, matchedID)
		b.reactionIndexMu.Lock()
		delete(b.reactionIndex, reaction.MessageID)
		b.reactionIndexMu.Unlock()
	default:
		logging.Debugf("zulip-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
	}
}

// sessionEventHandler reads agentd events for one session and translates them
// to Zulip actions. It runs in its own goroutine per session.
func (b *Bridge) sessionEventHandler(sess *sessionConn) {
	events := sess.conn.Events()
	var accumContent strings.Builder

	for {
		select {
		case <-b.stopChan:
			return

		case e, ok := <-events:
			if !ok {
				logging.Infof("zulip-bridge: agentd connection closed for session %s (key=%q)", sess.sessionID[:8], sess.key)
				b.removeSession(sess.key)
				return
			}
			b.handleEvent(sess, e, &accumContent)
		}
	}
}

// handleEvent dispatches a single agentd Event for the given session.
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
		// Wrap thinking in a Zulip spoiler block when present.
		if thinking != "" {
			content = fmt.Sprintf("```spoiler Thinking\n%s\n```\n\n%s",
				strings.TrimSpace(thinking), strings.TrimSpace(content))
		}

		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, content, nil); err != nil {
				logging.Errorf("zulip-bridge: send response: %v", err)
			}
		} else {
			logging.Warnf("zulip-bridge: DoneEvent but no current message context for session %s, dropping response", sess.sessionID[:8])
		}

	case agentproto.EventTypeToolInvoked:
		if e.ToolInvoked == nil {
			return
		}
		// Skip the informational notice when permission approval is required — the
		// PermRequest event that follows already describes the tool and its parameters.
		if e.ToolInvoked.RequiresPermission {
			return
		}
		if currentMsg != nil {
			if err := b.channel.AddReaction(*currentMsg, "wrench"); err != nil {
				logging.Debugf("zulip-bridge: add wrench reaction: %v", err)
			}
		}
		var paramsStr string
		if len(e.ToolInvoked.Params) > 0 {
			pretty, err := json.MarshalIndent(json.RawMessage(e.ToolInvoked.Params), "", "  ")
			if err == nil {
				paramsStr = string(pretty)
			} else {
				paramsStr = string(e.ToolInvoked.Params)
			}
		}
		detail := fmt.Sprintf("```spoiler Tool invoked: %s\n%s\n```", e.ToolInvoked.ToolName, paramsStr)
		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, detail, nil); err != nil {
				logging.Debugf("zulip-bridge: send tool-invoked notice: %v", err)
			}
		}

	case agentproto.EventTypeToolResult:
		// Tool results are informational; agentd handles them internally.

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
			if err := b.channel.SendResponse(*currentMsg, note, nil); err != nil {
				logging.Debugf("zulip-bridge: send perm-mention notice: %v", err)
			}
		}

	case agentproto.EventTypeCompaction:
		if e.Compaction == nil {
			return
		}
		note := fmt.Sprintf("_Conversation history compacted (%d → %d messages)._",
			e.Compaction.MessagesBefore, e.Compaction.MessagesAfter)
		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, note, nil); err != nil {
				logging.Debugf("zulip-bridge: send compaction notice: %v", err)
			}
		}

	case agentproto.EventTypeError:
		if e.Error == nil {
			return
		}
		errMsg := "Error: " + e.Error.Message
		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, errMsg, fmt.Errorf("%s", e.Error.Message)); err != nil {
				logging.Errorf("zulip-bridge: send error notice: %v", err)
			}
		}
		if e.Error.Fatal {
			logging.Errorf("zulip-bridge: fatal agentd error on session %s: %s", sess.sessionID[:8], e.Error.Message)
		}
	}
}

// handlePermRequest posts a Zulip permission-request message with ✅/❌ reactions
// and waits asynchronously for a reaction to arrive.
func (b *Bridge) handlePermRequest(sess *sessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("zulip-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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
	permMsg := fmt.Sprintf(
		"Assistant is requesting permission to employ tool `%s`.  Please review the details below.\n```spoiler Request Details\nTool's function is described as:\n````quote\n%s\n````\n\n### Parameters\n````\n%s\n````\n---\nPlease use a reaction to signal authorization intent.  Request will timeout after 60 seconds.\n```",
		req.ToolName,
		req.Description,
		paramsJSON,
	)

	msgID, err := b.channel.SendMessageWithID(*originalMsg, permMsg)
	if err != nil {
		logging.Warnf("zulip-bridge: send permission request message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	if msgID != 0 {
		time.Sleep(400 * time.Millisecond)
		if err := b.addReactionWithRetry(msgID, "check", 3); err != nil {
			logging.Warnf("zulip-bridge: add check reaction after retries: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
		if err := b.addReactionWithRetry(msgID, "x", 3); err != nil {
			logging.Warnf("zulip-bridge: add x reaction after retries: %v", err)
		}
	}

	// Register the pending permission in the session and the global reaction index.
	responseChan := make(chan bool, 1)
	perm := &pendingPerm{
		requestID:    req.RequestID,
		zulipMsgID:   msgID,
		originalMsg:  originalMsg,
		responseChan: responseChan,
		requestedAt:  time.Now(),
	}
	sess.permMu.Lock()
	sess.pendingPerms[req.RequestID] = perm
	sess.permMu.Unlock()

	b.reactionIndexMu.Lock()
	b.reactionIndex[msgID] = sess.key
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
			logging.Infof("zulip-bridge: permission %s resolved: approved=%v", shortID, approved)
		case <-time.After(approvalTimeout):
			logging.Warnf("zulip-bridge: permission %s timed out after %s; denying", shortID, approvalTimeout)
			approved = false
			sess.permMu.Lock()
			delete(sess.pendingPerms, req.RequestID)
			sess.permMu.Unlock()
			b.reactionIndexMu.Lock()
			delete(b.reactionIndex, msgID)
			b.reactionIndexMu.Unlock()
		case <-b.stopChan:
			approved = false
			sess.permMu.Lock()
			delete(sess.pendingPerms, req.RequestID)
			sess.permMu.Unlock()
			b.reactionIndexMu.Lock()
			delete(b.reactionIndex, msgID)
			b.reactionIndexMu.Unlock()
		}

		if err := sess.conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{
				RequestID: req.RequestID,
				Approved:  approved,
			},
		}); err != nil {
			logging.Errorf("zulip-bridge: send perm response: %v", err)
		}
	})
}

// addReactionWithRetry adds an emoji reaction to a Zulip message, retrying on
// transient failures with exponential backoff.
func (b *Bridge) addReactionWithRetry(msgID int, emoji string, maxAttempts int) error {
	const baseDelay = 300 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			logging.Debugf("zulip-bridge: retry %d/%d adding %q reaction to msg %d (backoff %v): %v",
				attempt+1, maxAttempts, emoji, msgID, delay, lastErr)
			time.Sleep(delay)
		}
		if err := b.channel.AddReactionByID(msgID, emoji); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// startEvictionLoop launches a goroutine that periodically evicts idle sessions.
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

// evictIdleSessions closes and removes sessions that have been idle past the TTL.
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

	logging.Infof("zulip-bridge: evicted %d idle sessions: %v", len(evicted), evicted)

	store, _ := loadSessionStore(b.sessFile)
	for _, key := range evicted {
		delete(store, key)
	}
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("zulip-bridge: save session store after eviction: %v", err)
	}
}
