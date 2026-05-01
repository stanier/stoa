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

	dg "github.com/bwmarrin/discordgo"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/discord"
	"github.com/stanier/stoa/internal/logging"
)

// mentionRe strips Discord @-mention markup (<@SNOWFLAKE> and <@!SNOWFLAKE>).
var mentionRe = regexp.MustCompile(`<@!?[0-9]+>`)

func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

// firstLine returns the first non-empty line of s, truncated to 100 characters.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

const (
	bridgePermissionTimeout = 60 * time.Second
	bridgeShutdownTimeout   = 15 * time.Second
)

// pendingPerm tracks a Discord permission request awaiting a reaction.
type pendingPerm struct {
	requestID    string
	channelID    string
	messageID    string // Discord snowflake ID of the permission-request message
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

type discordAuthStatus struct {
	userID      string
	authorized  bool
	roleName    string
	rolePersona string
	roleTools   []string
	cachedAt    time.Time
}

// Bridge connects Discord to agentd, sharding sessions by conversation context.
// DM conversations each get their own agentd session, keyed by Discord user ID.
type Bridge struct {
	cfg         *config.Config
	agentClient agentproto.Client
	channel     *channels.DiscordChannel
	stopChan    chan struct{}
	wg          sync.WaitGroup
	httpClient  *http.Client

	authdURL     string
	serviceToken string

	authStatusCache   map[string]*discordAuthStatus
	authStatusCacheMu sync.Mutex

	sessions map[string]*sessionConn
	mu       sync.RWMutex
	idleTTL  time.Duration
	sessFile string

	// reactionIndex maps channelID+":"+messageID → conversationKey
	reactionIndex   map[string]string
	reactionIndexMu sync.Mutex
}

// NewBridge creates a Bridge.
func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.DiscordChannel) *Bridge {
	idleTTL := cfg.Discord.SessionTTL
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
		authStatusCache: make(map[string]*discordAuthStatus),
		sessions:        make(map[string]*sessionConn),
		idleTTL:         idleTTL,
		sessFile:        cfg.Discord.SessionFile,
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

// conversationKey returns a stable session key for a Discord conversation.
//   - DM:     "dm:<discord_user_id>"
//   - Thread: "thread:<thread_channel_id>"
//   - Guild:  keyed dynamically after thread creation (not called for plain guild msgs)
func conversationKey(msg channels.Message) string {
	switch msg.Type {
	case "thread":
		return "thread:" + msg.Topic // Topic carries the thread ID
	default:
		return "dm:" + msg.SenderID
	}
}

// reactionKey returns the map key used in reactionIndex.
func reactionKey(channelID, messageID string) string {
	return channelID + ":" + messageID
}

// --- authd integration ---

func (b *Bridge) fetchAuthStatus(ctx context.Context, discordUserID string) (userID string, authorized bool) {
	if discordUserID == "" || b.authdURL == "" || b.serviceToken == "" {
		return "", false
	}

	b.authStatusCacheMu.Lock()
	if cached, ok := b.authStatusCache[discordUserID]; ok && time.Since(cached.cachedAt) < authStatusCacheTTL {
		b.authStatusCacheMu.Unlock()
		return cached.userID, cached.authorized
	}
	b.authStatusCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/discord/status?discord_user_id="+discordUserID, nil)
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
		b.authStatusCache[discordUserID] = &discordAuthStatus{cachedAt: time.Now()}
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

	entry := &discordAuthStatus{
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
	b.authStatusCache[discordUserID] = entry
	b.authStatusCacheMu.Unlock()

	return result.UserID, result.Authorized
}

func (b *Bridge) resolveAccessPolicy(ctx context.Context, discordUserID string) config.DiscordRolePolicy {
	if b.authdURL == "" || b.serviceToken == "" {
		return config.DiscordRolePolicy{Allow: true, Persona: b.cfg.Discord.Persona}
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[discordUserID]
	b.authStatusCacheMu.Unlock()
	if !ok || time.Since(cached.cachedAt) >= authStatusCacheTTL {
		b.fetchAuthStatus(ctx, discordUserID)
		b.authStatusCacheMu.Lock()
		cached = b.authStatusCache[discordUserID]
		b.authStatusCacheMu.Unlock()
	}

	if cached == nil || cached.userID == "" || !cached.authorized {
		return b.cfg.Discord.AccessPolicy.Unauthenticated
	}

	if cached.roleName != "" {
		logging.Debugf("discord-bridge: %s matched DB role %q", discordUserID, cached.roleName)
		return config.DiscordRolePolicy{
			Allow:        true,
			Persona:      cached.rolePersona,
			AllowedTools: cached.roleTools,
		}
	}

	return config.DiscordRolePolicy{Allow: true, Persona: b.cfg.Discord.Persona}
}

func (b *Bridge) resolveUserID(ctx context.Context, discordUserID string) string {
	if discordUserID == "" || b.authdURL == "" || b.serviceToken == "" {
		return ""
	}

	b.authStatusCacheMu.Lock()
	cached, ok := b.authStatusCache[discordUserID]
	b.authStatusCacheMu.Unlock()
	if ok && cached.userID != "" {
		return cached.userID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.authdURL+"/link/discord/resolve?discord_user_id="+discordUserID, nil)
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
			logging.Infof("discord-bridge: reconnected to existing session %s for key %q", existingID[:8], key)
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
		logging.Warnf("discord-bridge: persisted session %s for key %q no longer valid (%v); creating new", existingID[:8], key, err)
	}

	var err error
	sessionID, err = b.agentClient.CreateSession(ctx, agentproto.SessionConfig{
		Persona:      persona,
		Source:       "discord",
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
	logging.Infof("discord-bridge: new session %s for key %q (persona=%q)", sessionID[:8], key, persona)

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
		logging.Warnf("discord-bridge: save session store: %v", err)
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
		delete(b.reactionIndex, reactionKey(p.channelID, p.messageID))
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
		logging.Info("discord-bridge: stopped gracefully")
	case <-time.After(bridgeShutdownTimeout):
		logging.Warn("discord-bridge: shutdown timed out")
	}
}

// --- message handler ---

func (b *Bridge) messageHandler() {
	commandPrefix := b.cfg.Discord.CommandPrefix
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

			logging.Infof("discord-bridge: received message from %s type=%s (len=%d)", msg.SenderID, msg.Type, len(msg.Content))

			// Accept DMs, threads, and guild channel messages.
			// Drop anything else (e.g. unknown channel types).
			if msg.Type != "direct" && msg.Type != "thread" && msg.Type != "guild" {
				continue
			}

			// For guild channel messages (not yet in a thread), create a thread
			// and re-route the message into it before processing.
			if msg.Type == "guild" {
				raw, ok := msg.Raw.(*discord.MessageEvent)
				if !ok {
					logging.Warnf("discord-bridge: guild message missing raw event, skipping")
					continue
				}
				threadName := firstLine(msg.Content)
				if threadName == "" {
					threadName = "Conversation"
				}
				threadID, err := b.channel.StartThread(raw.ChannelID, threadName)
				if err != nil {
					logging.Errorf("discord-bridge: create thread in %s: %v", raw.ChannelID, err)
					continue
				}
				logging.Infof("discord-bridge: created thread %s in channel %s", threadID, raw.ChannelID)
				// Rewrite the message to look like a thread message.
				raw.ThreadID = threadID
				raw.IsThread = true
				raw.IsGuildChannel = false
				raw.ChannelID = threadID
				msg.Type = "thread"
				msg.Topic = threadID
				msg.Recipient = threadID
			}

			key := conversationKey(msg)
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

			// Resolve access policy using Discord user ID directly.
			policy := b.resolveAccessPolicy(context.Background(), msg.SenderID)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.cfg.Discord.Persona
			}

			agentUserID := b.resolveUserID(context.Background(), msg.SenderID)

			sess, err := b.getOrCreateSession(context.Background(), key, effectivePersona, agentUserID, policy.AllowedTools)
			if err != nil {
				logging.Errorf("discord-bridge: get/create session for %q: %v", key, err)
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
				logging.Errorf("discord-bridge: send to agentd session %s: %v", sess.sessionID[:8], err)
			}

		case status := <-b.channel.StatusUpdates():
			if status.Connected {
				logging.Info("discord-bridge: Discord connected")
			} else {
				logging.Errorf("discord-bridge: Discord disconnected: %v", status.Error)
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

	body, _ := json.Marshal(map[string]string{"discord_user_id": msg.SenderID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.authdURL+"/link/discord/start", bytes.NewReader(body))
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
		"To link your Discord account, visit this URL within %d minutes:\n\n%s\n\nAfter signing in you'll receive a confirmation here.",
		minutes, result.URL,
	), nil)

	deadline := time.Duration(result.ExpiresIn) * time.Second
	go b.pollForLink(msg, msg.SenderID, deadline)
}

func (b *Bridge) pollForLink(msg channels.Message, discordUserID string, deadline time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	timer := time.NewTimer(deadline)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case <-ticker.C:
			userID := b.resolveUserID(context.Background(), discordUserID)
			if userID == "" {
				continue
			}
			b.authStatusCacheMu.Lock()
			if e, ok := b.authStatusCache[discordUserID]; ok {
				e.userID = userID
			}
			b.authStatusCacheMu.Unlock()

			_ = b.channel.SendResponse(msg,
				"✅ Your Discord account has been linked! Future conversations will be associated with your account.", nil)
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
		b.authdURL+"/link/discord/unlink?discord_user_id="+msg.SenderID, nil)
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
		"Your Discord account has been unlinked. Future conversations will no longer be associated with your account.", nil)
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
			"Your Discord account is **not linked**.\nUse `!link` to link it.", nil)
		return
	}

	_ = b.channel.SendResponse(msg,
		"✅ Your Discord account is **linked** to your account.", nil)
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
			logging.Warnf("discord-bridge: get messages before clear: %v", err)
		}
		if err := b.agentClient.ClearSession(context.Background(), sess.sessionID); err != nil {
			logging.Errorf("discord-bridge: clear session: %v", err)
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
			logging.Errorf("discord-bridge: compact session: %v", err)
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
			logging.Errorf("discord-bridge: get tokens: %v", err)
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
			logging.Errorf("discord-bridge: restore messages: %v", err)
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
				"- `%slink` — link your Discord account to the auth system\n"+
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

func (b *Bridge) handleReaction(reaction discord.ReactionEvent) {
	// Accept standard Unicode check/x variants.
	emoji := reaction.Emoji
	if emoji != "✅" && emoji != "white_check_mark" && emoji != "heavy_check_mark" && emoji != "❌" && emoji != "x" {
		return
	}
	approved := emoji != "❌" && emoji != "x"

	logging.Debugf("discord-bridge: reaction %q (approved=%v) on %s:%s", emoji, approved, reaction.ChannelID, reaction.MessageID)

	rk := reactionKey(reaction.ChannelID, reaction.MessageID)
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
		if p.channelID == reaction.ChannelID && p.messageID == reaction.MessageID {
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
			logging.Infof("discord-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("discord-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.pendingPerms, matchedID)
		b.reactionIndexMu.Lock()
		delete(b.reactionIndex, rk)
		b.reactionIndexMu.Unlock()
	default:
		logging.Debugf("discord-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
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
				logging.Infof("discord-bridge: agentd connection closed for session %s (key=%q)", sess.sessionID[:8], sess.key)
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
			b.sendSplitResponse(*currentMsg, content)
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
		embed := &dg.MessageEmbed{
			Title:       "Tool Invoked: " + e.ToolInvoked.ToolName,
			Description: "```json\n" + paramsStr + "\n```",
			Color:       0x5865F2, // blue
		}
		if currentMsg != nil {
			_, _ = b.channel.SendEmbed(*currentMsg, embed)
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
		embed := &dg.MessageEmbed{
			Title:       "Tool ran automatically: " + e.PermMention.ToolName,
			Description: e.PermMention.Description,
			Color:       0x57F287, // green
		}
		if currentMsg != nil {
			_, _ = b.channel.SendEmbed(*currentMsg, embed)
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
		embed := &dg.MessageEmbed{
			Title:       "Error",
			Description: e.Error.Message,
			Color:       0xED4245, // red
		}
		if currentMsg != nil {
			_, _ = b.channel.SendEmbed(*currentMsg, embed)
		}
		if e.Error.Fatal {
			logging.Errorf("discord-bridge: fatal agentd error on session %s: %s", sess.sessionID[:8], e.Error.Message)
		}
	}
}

// --- permission request ---

func (b *Bridge) handlePermRequest(sess *sessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("discord-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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

	embed := &dg.MessageEmbed{
		Title:       "Permission Request: " + req.ToolName,
		Description: req.Description,
		Fields: []*dg.MessageEmbedField{
			{Name: "Parameters", Value: "```json\n" + paramsJSON + "\n```", Inline: false},
			{Name: "Instructions", Value: "React with ✅ to approve or ❌ to deny. Times out in 60 seconds.", Inline: false},
		},
		Color: 0xFEE75C, // yellow
	}

	msgID, err := b.channel.SendEmbed(*originalMsg, embed)
	if err != nil {
		logging.Warnf("discord-bridge: send permission request message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	raw, _ := originalMsg.Raw.(*discord.MessageEvent)
	channelID := ""
	if raw != nil {
		channelID = raw.ChannelID
	}

	if channelID != "" && msgID != "" {
		time.Sleep(400 * time.Millisecond)
		if err := b.addReactionWithRetry(channelID, msgID, "✅", 3); err != nil {
			logging.Warnf("discord-bridge: add check reaction after retries: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
		if err := b.addReactionWithRetry(channelID, msgID, "❌", 3); err != nil {
			logging.Warnf("discord-bridge: add x reaction after retries: %v", err)
		}
	}

	responseChan := make(chan bool, 1)
	perm := &pendingPerm{
		requestID:    req.RequestID,
		channelID:    channelID,
		messageID:    msgID,
		originalMsg:  originalMsg,
		responseChan: responseChan,
		requestedAt:  time.Now(),
	}
	sess.permMu.Lock()
	sess.pendingPerms[req.RequestID] = perm
	sess.permMu.Unlock()

	rk := reactionKey(channelID, msgID)
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
			logging.Infof("discord-bridge: permission %s resolved: approved=%v", shortID, approved)
		case <-time.After(approvalTimeout):
			logging.Warnf("discord-bridge: permission %s timed out after %s; denying", shortID, approvalTimeout)
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
			logging.Errorf("discord-bridge: send perm response: %v", err)
		}
	})
}

func (b *Bridge) addReactionWithRetry(channelID, messageID, emoji string, maxAttempts int) error {
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
		if err := b.channel.AddReactionByID(channelID, messageID, emoji); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// --- message splitting ---

// sendSplitResponse splits a potentially long LLM response into Discord-safe
// chunks (≤2000 characters) and sends each one. For thread messages the chunks
// are sent to the thread channel so they are grouped together.
func (b *Bridge) sendSplitResponse(msg channels.Message, content string) {
	chunks := discord.SplitMessage(content, discord.DefaultMessageLimit)

	// Determine the target channel ID. For thread messages the raw event's
	// ChannelID will already point at the thread (we rewrote it above).
	raw, isDiscord := msg.Raw.(*discord.MessageEvent)
	useThreadChannel := isDiscord && raw.IsThread && raw.ThreadID != ""

	for i, chunk := range chunks {
		if useThreadChannel {
			if _, err := b.channel.SendMessageToChannelWithID(raw.ThreadID, chunk); err != nil {
				logging.Errorf("discord-bridge: send response chunk %d/%d to thread %s: %v",
					i+1, len(chunks), raw.ThreadID, err)
			}
		} else {
			if err := b.channel.SendResponse(msg, chunk, nil); err != nil {
				logging.Errorf("discord-bridge: send response chunk %d/%d: %v", i+1, len(chunks), err)
			}
		}
	}
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

	logging.Infof("discord-bridge: evicted %d idle sessions: %v", len(evicted), evicted)

	store, _ := loadSessionStore(b.sessFile)
	for _, key := range evicted {
		delete(store, key)
	}
	if err := saveSessionStore(b.sessFile, store); err != nil {
		logging.Warnf("discord-bridge: save session store after eviction: %v", err)
	}
}
