package bridges

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
)

// PendingPermCore holds the platform-independent state for a pending permission request.
// ReactionKey is an opaque string stored in BaseBridge.ReactionIndex; each platform
// computes it in whatever format its reaction handler expects.
type PendingPermCore struct {
	RequestID    string
	ReactionKey  string
	OriginalMsg  *channels.Message
	ResponseChan chan bool
	RequestedAt  time.Time
}

// SessionConn holds all per-conversation state for one agentd session.
// ThreadRootID and RoomID are set by the Matrix bridge for threaded replies;
// they are zero-value for all other platforms.
type SessionConn struct {
	Key       string
	SessionID string
	Conn      agentproto.ClientConn

	LastActivity   time.Time
	LastActivityMu sync.Mutex

	CurrentMsg   *channels.Message
	CurrentMsgMu sync.RWMutex

	PendingPerms map[string]*PendingPermCore
	PermMu       sync.Mutex

	LastSnapshot []chat.Message
	SnapshotMu   sync.Mutex

	// Matrix-specific; zero-value for all other platforms.
	ThreadRootID string
	RoomID       string
}

func (s *SessionConn) TouchActivity() {
	s.LastActivityMu.Lock()
	s.LastActivity = time.Now()
	s.LastActivityMu.Unlock()
}

func (s *SessionConn) GetLastActivity() time.Time {
	s.LastActivityMu.Lock()
	defer s.LastActivityMu.Unlock()
	return s.LastActivity
}

// AuthStatusCacheTTL is the time-to-live for cached auth-status entries.
const AuthStatusCacheTTL = 5 * time.Minute

// AuthStatus caches the result of an authd platform-status call.
type AuthStatus struct {
	UserID      string
	Authorized  bool
	RoleName    string
	RolePersona string
	RoleTools   []string
	CachedAt    time.Time
}

// RolePolicy is the unified access-control policy applied to one user.
// It replaces per-platform ZulipRolePolicy / SlackRolePolicy / etc.
type RolePolicy struct {
	Allow        bool
	Persona      string
	AllowedTools []string
}

// PlatformConfig parameterises a BaseBridge for a specific chat platform.
type PlatformConfig struct {
	Name        string // log prefix, e.g. "zulip-bridge"
	Source      string // agentd SessionConfig.Source, e.g. "zulip"
	Platform    string // human-readable, e.g. "Zulip"
	StatusPath  string // e.g. "/link/zulip/status"
	StatusParam string // URL query param name for the identifier, e.g. "email"
	StartPath   string // e.g. "/link/zulip/start"
	LinkBodyKey string // JSON body key, e.g. "zulip_email"
	ResolvePath string // e.g. "/link/zulip/resolve"
	UnlinkPath  string // e.g. "/link/zulip/unlink"
	// DmType restricts auth commands to this msg.Type value; empty = no restriction.
	DmType          string
	ShowIdentifier  bool // include identifier in status/unlink messages
	DefaultPersona  string
	UnauthPolicy    RolePolicy
	ApprovalTimeout time.Duration
	CommandPrefix   string
	SessionTTL      time.Duration
	SessionFile     string
	Bold            func(string) string // wraps text in platform bold markup
	Bullet          string              // list bullet, e.g. "- " or "• "
}

const bridgeShutdownTimeout = 15 * time.Second

// BaseBridge contains the shared state and behaviour for all chat-platform bridges.
type BaseBridge struct {
	Platform     PlatformConfig
	Channel      channels.Channel
	AgentClient  agentproto.Client
	Cfg          *config.Config
	StopChan     chan struct{}
	WG           sync.WaitGroup
	HTTPClient   *http.Client
	AuthdURL     string
	ServiceToken string

	AuthCache   map[string]*AuthStatus
	AuthCacheMu sync.Mutex

	Sessions map[string]*SessionConn
	Mu       sync.RWMutex

	ReactionIndex   map[string]string
	ReactionIndexMu sync.Mutex
}

// NewBaseBridge constructs a BaseBridge. authdURL and serviceToken are read
// from AGENTD_AUTHD_URL / AGENTD_API_KEY environment variables.
func NewBaseBridge(cfg *config.Config, platform PlatformConfig, channel channels.Channel, agentClient agentproto.Client) *BaseBridge {
	return &BaseBridge{
		Platform:      platform,
		Channel:       channel,
		AgentClient:   agentClient,
		Cfg:           cfg,
		StopChan:      make(chan struct{}),
		HTTPClient:    &http.Client{Timeout: 5 * time.Second},
		AuthdURL:      strings.TrimRight(os.Getenv("AGENTD_AUTHD_URL"), "/"),
		ServiceToken:  os.Getenv("AGENTD_API_KEY"),
		AuthCache:     make(map[string]*AuthStatus),
		Sessions:      make(map[string]*SessionConn),
		ReactionIndex: make(map[string]string),
	}
}

// RecoverPanic logs a panic and its stack trace. Intended for use via defer.
func RecoverPanic(ctx string) {
	if r := recover(); r != nil {
		logging.Errorf("Panic recovered in %s: %v\n%s", ctx, r, debug.Stack())
	}
}

// SpawnRecoverable starts fn in a tracked goroutine with panic recovery.
func (b *BaseBridge) SpawnRecoverable(name string, fn func()) {
	b.WG.Add(1)
	go func() {
		defer b.WG.Done()
		defer RecoverPanic(name)
		fn()
	}()
}

// Stop closes all session connections, signals goroutines to exit, and waits.
func (b *BaseBridge) Stop() {
	close(b.StopChan)

	b.Mu.Lock()
	for _, sess := range b.Sessions {
		_ = sess.Conn.Close()
	}
	b.Mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.WG.Wait()
		close(done)
	}()

	select {
	case <-done:
		logging.Infof("%s: stopped gracefully", b.Platform.Name)
	case <-time.After(bridgeShutdownTimeout):
		logging.Warnf("%s: shutdown timed out", b.Platform.Name)
	}
}

// GetOrCreateSession returns the existing session for key, or creates one.
// spawnEventHandler is launched (in a recoverable goroutine) for new/reconnected sessions.
func (b *BaseBridge) GetOrCreateSession(
	ctx context.Context,
	key, persona, userID string,
	allowedTools []string,
	spawnEventHandler func(*SessionConn),
) (*SessionConn, error) {
	b.Mu.RLock()
	sess, ok := b.Sessions[key]
	b.Mu.RUnlock()
	if ok {
		sess.TouchActivity()
		return sess, nil
	}

	b.Mu.Lock()
	defer b.Mu.Unlock()

	if sess, ok = b.Sessions[key]; ok {
		sess.TouchActivity()
		return sess, nil
	}

	store, _ := LoadSessionStore(b.Platform.SessionFile)
	if existingID, found := store[key]; found {
		conn, err := b.AgentClient.Connect(ctx, existingID)
		if err == nil {
			logging.Infof("%s: reconnected to existing session %s for key %q", b.Platform.Name, existingID[:8], key)
			sess = &SessionConn{
				Key:          key,
				SessionID:    existingID,
				Conn:         conn,
				LastActivity: time.Now(),
				PendingPerms: make(map[string]*PendingPermCore),
			}
			b.Sessions[key] = sess
			b.SpawnRecoverable("eventHandler:"+key, func() { spawnEventHandler(sess) })
			return sess, nil
		}
		logging.Warnf("%s: persisted session %s for key %q no longer valid (%v); creating new",
			b.Platform.Name, existingID[:8], key, err)
	}

	sessionID, err := b.AgentClient.CreateSession(ctx, agentproto.SessionConfig{
		Persona:      persona,
		Source:       b.Platform.Source,
		UserID:       userID,
		AllowedTools: allowedTools,
	})
	if err != nil {
		return nil, fmt.Errorf("create agentd session for %q: %w", key, err)
	}
	conn, err := b.AgentClient.Connect(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("connect to agentd session %s: %w", sessionID, err)
	}
	logging.Infof("%s: new session %s for key %q (persona=%q)", b.Platform.Name, sessionID[:8], key, persona)

	sess = &SessionConn{
		Key:          key,
		SessionID:    sessionID,
		Conn:         conn,
		LastActivity: time.Now(),
		PendingPerms: make(map[string]*PendingPermCore),
	}
	b.Sessions[key] = sess

	store[key] = sessionID
	if err := SaveSessionStore(b.Platform.SessionFile, b.Platform.Name+"-", store); err != nil {
		logging.Warnf("%s: save session store: %v", b.Platform.Name, err)
	}

	b.SpawnRecoverable("eventHandler:"+key, func() { spawnEventHandler(sess) })
	return sess, nil
}

// RemoveSession removes a session and cleans up its ReactionIndex entries.
func (b *BaseBridge) RemoveSession(key string) {
	b.Mu.Lock()
	sess, ok := b.Sessions[key]
	if ok {
		delete(b.Sessions, key)
	}
	b.Mu.Unlock()

	if !ok || sess == nil {
		return
	}

	b.ReactionIndexMu.Lock()
	sess.PermMu.Lock()
	for _, p := range sess.PendingPerms {
		if p.ReactionKey != "" {
			delete(b.ReactionIndex, p.ReactionKey)
		}
	}
	sess.PermMu.Unlock()
	b.ReactionIndexMu.Unlock()
}

// --- authd integration ---

// FetchAuthStatus calls the platform status endpoint and caches the result.
// identifier is the platform-native user key (email, discord_user_id, etc.).
func (b *BaseBridge) FetchAuthStatus(ctx context.Context, identifier string) (userID string, authorized bool) {
	if identifier == "" || b.AuthdURL == "" || b.ServiceToken == "" {
		return "", false
	}

	b.AuthCacheMu.Lock()
	if cached, ok := b.AuthCache[identifier]; ok && time.Since(cached.CachedAt) < AuthStatusCacheTTL {
		b.AuthCacheMu.Unlock()
		return cached.UserID, cached.Authorized
	}
	b.AuthCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.AuthdURL+b.Platform.StatusPath+"?"+b.Platform.StatusParam+"="+identifier, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+b.ServiceToken)

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		b.AuthCacheMu.Lock()
		b.AuthCache[identifier] = &AuthStatus{CachedAt: time.Now()}
		b.AuthCacheMu.Unlock()
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

	entry := &AuthStatus{
		UserID:     result.UserID,
		Authorized: result.Authorized,
		CachedAt:   time.Now(),
	}
	if result.Role != nil {
		entry.RoleName    = result.Role.Name
		entry.RolePersona = result.Role.Persona
		entry.RoleTools   = result.Role.AllowedTools
	}

	b.AuthCacheMu.Lock()
	b.AuthCache[identifier] = entry
	b.AuthCacheMu.Unlock()

	return result.UserID, result.Authorized
}

// ResolveAccessPolicy returns the effective RolePolicy for identifier.
func (b *BaseBridge) ResolveAccessPolicy(ctx context.Context, identifier string) RolePolicy {
	if b.AuthdURL == "" || b.ServiceToken == "" {
		return RolePolicy{Allow: true, Persona: b.Platform.DefaultPersona}
	}

	b.AuthCacheMu.Lock()
	cached, ok := b.AuthCache[identifier]
	b.AuthCacheMu.Unlock()
	if !ok || time.Since(cached.CachedAt) >= AuthStatusCacheTTL {
		b.FetchAuthStatus(ctx, identifier)
		b.AuthCacheMu.Lock()
		cached = b.AuthCache[identifier]
		b.AuthCacheMu.Unlock()
	}

	if cached == nil || cached.UserID == "" || !cached.Authorized {
		return b.Platform.UnauthPolicy
	}

	if cached.RoleName != "" {
		logging.Debugf("%s: %s matched DB role %q", b.Platform.Name, identifier, cached.RoleName)
		return RolePolicy{
			Allow:        true,
			Persona:      cached.RolePersona,
			AllowedTools: cached.RoleTools,
		}
	}

	return RolePolicy{Allow: true, Persona: b.Platform.DefaultPersona}
}

// ResolveUserID returns the authd user_id for identifier, or "" if not linked.
func (b *BaseBridge) ResolveUserID(ctx context.Context, identifier string) string {
	if identifier == "" || b.AuthdURL == "" || b.ServiceToken == "" {
		return ""
	}

	b.AuthCacheMu.Lock()
	cached, ok := b.AuthCache[identifier]
	b.AuthCacheMu.Unlock()
	if ok && cached.UserID != "" {
		return cached.UserID
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.AuthdURL+b.Platform.ResolvePath+"?"+b.Platform.StatusParam+"="+identifier, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+b.ServiceToken)

	resp, err := b.HTTPClient.Do(req)
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

	if result.UserID != "" {
		b.AuthCacheMu.Lock()
		if e := b.AuthCache[identifier]; e != nil {
			e.UserID = result.UserID
		} else {
			b.AuthCache[identifier] = &AuthStatus{UserID: result.UserID, CachedAt: time.Now()}
		}
		b.AuthCacheMu.Unlock()
	}
	return result.UserID
}

// --- auth commands ---

// HandleAuthCommand handles !link: starts the account-linking flow.
// identifier is the platform-native user key (email or user ID).
func (b *BaseBridge) HandleAuthCommand(ctx context.Context, msg channels.Message, identifier string) {
	if b.Platform.DmType != "" && msg.Type != b.Platform.DmType {
		_ = b.Channel.SendResponse(msg, "Account linking only works in a direct message with the bot.", nil)
		return
	}
	if b.AuthdURL == "" || b.ServiceToken == "" {
		_ = b.Channel.SendResponse(msg, "Account linking is not configured on this server.", nil)
		return
	}
	if identifier == "" {
		_ = b.Channel.SendResponse(msg,
			fmt.Sprintf("Could not determine your %s account identifier.", b.Platform.Platform), nil)
		return
	}

	body, _ := json.Marshal(map[string]string{b.Platform.LinkBodyKey: identifier})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.AuthdURL+b.Platform.StartPath, bytes.NewReader(body))
	if err != nil {
		_ = b.Channel.SendResponse(msg, "Error starting link flow.", nil)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.ServiceToken)

	resp, err := b.HTTPClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		_ = b.Channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	defer resp.Body.Close()

	var result struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.URL == "" {
		_ = b.Channel.SendResponse(msg, "Error parsing auth server response.", nil)
		return
	}

	minutes := result.ExpiresIn / 60
	_ = b.Channel.SendResponse(msg, fmt.Sprintf(
		"To link your %s account, visit this URL within %d minutes:\n\n%s\n\n"+
			"After signing in you'll receive a confirmation here.",
		b.Platform.Platform, minutes, result.URL,
	), nil)

	deadline := time.Duration(result.ExpiresIn) * time.Second
	go b.PollForLink(msg, identifier, deadline)
}

// PollForLink polls until the link for identifier is established, then confirms.
func (b *BaseBridge) PollForLink(msg channels.Message, identifier string, deadline time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	timer := time.NewTimer(deadline)
	defer ticker.Stop()
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			return
		case <-ticker.C:
			userID := b.ResolveUserID(context.Background(), identifier)
			if userID == "" {
				continue
			}
			b.AuthCacheMu.Lock()
			if e, ok := b.AuthCache[identifier]; ok {
				e.UserID = userID
			}
			b.AuthCacheMu.Unlock()

			_ = b.Channel.SendResponse(msg, fmt.Sprintf(
				"✅ Your %s account has been linked! "+
					"Future conversations will be associated with your account.",
				b.Platform.Platform,
			), nil)
			return
		}
	}
}

// HandleUnlinkCommand handles !unlink: removes the account link.
func (b *BaseBridge) HandleUnlinkCommand(ctx context.Context, msg channels.Message, identifier string) {
	if b.Platform.DmType != "" && msg.Type != b.Platform.DmType {
		_ = b.Channel.SendResponse(msg, "Account unlinking only works in a direct message with the bot.", nil)
		return
	}
	if b.AuthdURL == "" || b.ServiceToken == "" || identifier == "" {
		_ = b.Channel.SendResponse(msg, "Account linking is not configured on this server.", nil)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		b.AuthdURL+b.Platform.UnlinkPath+"?"+b.Platform.StatusParam+"="+identifier, nil)
	if err != nil {
		_ = b.Channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.ServiceToken)

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		_ = b.Channel.SendResponse(msg, "Error contacting auth server.", nil)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		_ = b.Channel.SendResponse(msg, "Failed to unlink account.", nil)
		return
	}

	b.AuthCacheMu.Lock()
	delete(b.AuthCache, identifier)
	b.AuthCacheMu.Unlock()

	_ = b.Channel.SendResponse(msg, fmt.Sprintf(
		"Your %s account has been unlinked. "+
			"Future conversations will no longer be associated with your account.",
		b.Platform.Platform,
	), nil)
}

// HandleStatusCommand handles !status / !whoami: shows account link state.
func (b *BaseBridge) HandleStatusCommand(ctx context.Context, msg channels.Message, identifier string) {
	if b.Platform.DmType != "" && msg.Type != b.Platform.DmType {
		_ = b.Channel.SendResponse(msg, "Account status only works in a direct message with the bot.", nil)
		return
	}
	if b.AuthdURL == "" || b.ServiceToken == "" || identifier == "" {
		_ = b.Channel.SendResponse(msg, "Account linking is not configured on this server.", nil)
		return
	}

	idSuffix := ""
	if b.Platform.ShowIdentifier {
		idSuffix = fmt.Sprintf(" (%s)", identifier)
	}

	userID := b.ResolveUserID(ctx, identifier)
	if userID == "" {
		_ = b.Channel.SendResponse(msg, fmt.Sprintf(
			"Your %s account%s is %s.\nUse `!link` to link it.",
			b.Platform.Platform, idSuffix, b.Platform.Bold("not linked"),
		), nil)
		return
	}

	b.AuthCacheMu.Lock()
	if e, ok := b.AuthCache[identifier]; ok {
		e.UserID = userID
	}
	b.AuthCacheMu.Unlock()

	_ = b.Channel.SendResponse(msg, fmt.Sprintf(
		"✅ Your %s account%s is %s to your account.",
		b.Platform.Platform, idSuffix, b.Platform.Bold("linked"),
	), nil)
}

// --- local bridge commands ---

// HandleCommand dispatches a local bridge command parsed from the message text.
func (b *BaseBridge) HandleCommand(sess *SessionConn, msg channels.Message, raw, prefix string) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		_ = b.Channel.SendResponse(msg,
			fmt.Sprintf("No command specified. Use `%shelp` for available commands.", prefix), nil)
		return
	}
	cmd := strings.ToLower(parts[0])
	bold := b.Platform.Bold
	bullet := b.Platform.Bullet
	name := b.Platform.Name

	switch cmd {
	case "clear":
		snapshot, err := b.AgentClient.GetMessages(context.Background(), sess.SessionID)
		if err != nil {
			logging.Warnf("%s: get messages before clear: %v", name, err)
		}
		if err := b.AgentClient.ClearSession(context.Background(), sess.SessionID); err != nil {
			logging.Errorf("%s: clear session: %v", name, err)
			_ = b.Channel.SendResponse(msg, "Error clearing conversation: "+err.Error(), nil)
		} else {
			sess.SnapshotMu.Lock()
			sess.LastSnapshot = snapshot
			sess.SnapshotMu.Unlock()
			if len(snapshot) > 0 {
				text := fmt.Sprintf(
					"Conversation cleared.  %s: `%s`.  %d messages saved. Use `%sresume` to restore.",
					bold("Session ID"), sess.SessionID, len(snapshot), prefix)
				logging.Infof("%s: session %s cleared (snapshot: %d messages)", name, sess.SessionID[:8], len(snapshot))
				_ = b.Channel.SendResponse(msg, text, nil)
			}
		}

	case "compact":
		resp, err := b.AgentClient.CompactSession(context.Background(), sess.SessionID)
		if err != nil {
			logging.Errorf("%s: compact session: %v", name, err)
			_ = b.Channel.SendResponse(msg, "Error compacting conversation: "+err.Error(), nil)
		} else {
			logging.Infof("%s: session %s compacted", name, sess.SessionID[:8])
			text := fmt.Sprintf(
				"Conversation compacted.\n%s: %d messages, %d tokens\n%s: %d messages, %d tokens\n%s: %d tokens",
				bold("Before"), resp.MessagesBefore, resp.TokensBefore,
				bold("After"), resp.MessagesAfter, resp.TokensAfter,
				bold("Saved"), resp.TokensBefore-resp.TokensAfter)
			_ = b.Channel.SendResponse(msg, text, nil)
		}

	case "tokens":
		resp, err := b.AgentClient.GetTokens(context.Background(), sess.SessionID)
		if err != nil {
			logging.Errorf("%s: get tokens: %v", name, err)
			_ = b.Channel.SendResponse(msg, "Error getting token counts: "+err.Error(), nil)
		} else {
			_ = b.Channel.SendResponse(msg, agentproto.FormatTokensResponse(resp), nil)
		}

	case "resume":
		sess.SnapshotMu.Lock()
		snapshot := sess.LastSnapshot
		sess.SnapshotMu.Unlock()
		if len(snapshot) == 0 {
			_ = b.Channel.SendResponse(msg,
				fmt.Sprintf("No saved conversation to resume. Use `%sclear` first to save a snapshot.", prefix), nil)
			return
		}
		if err := b.AgentClient.RestoreMessages(context.Background(), sess.SessionID, snapshot); err != nil {
			logging.Errorf("%s: restore messages: %v", name, err)
			_ = b.Channel.SendResponse(msg, "Error restoring conversation: "+err.Error(), nil)
		} else {
			logging.Infof("%s: session %s restored (%d messages)", name, sess.SessionID[:8], len(snapshot))
			_ = b.Channel.SendResponse(msg, fmt.Sprintf("Conversation restored (%d messages).", len(snapshot)), nil)
		}

	case "model":
		providerType, providerModel := "", ""
		if dp := b.Cfg.LLM.DefaultProvider(); dp != nil {
			providerType, providerModel = dp.Type, dp.Model
		}
		_ = b.Channel.SendResponse(msg, fmt.Sprintf(
			"%s: `%s`  %s: `%s`",
			bold("Provider"), providerType, bold("Model"), providerModel,
		), nil)

	case "help":
		dmOnly := ""
		if b.Platform.DmType != "" {
			dmOnly = " (DM only)"
		}
		_ = b.Channel.SendResponse(msg, fmt.Sprintf(
			"%s (prefix: `%s`)\n\n"+
				"%s`%sclear` — clear conversation history (saves a snapshot)\n"+
				"%s`%sresume` — restore the last cleared conversation\n"+
				"%s`%scompact` — compact conversation history\n"+
				"%s`%stokens` — show token usage for current conversation\n"+
				"%s`%smodel` — show current LLM provider and model\n"+
				"%s`%slink` — link your %s account to the auth system%s\n"+
				"%s`%sunlink` — remove your account link%s\n"+
				"%s`%sstatus` — show your account link status%s\n"+
				"%s`%shelp` — show this message",
			bold("Available commands"), prefix,
			bullet, prefix, bullet, prefix, bullet, prefix,
			bullet, prefix, bullet, prefix,
			bullet, prefix, b.Platform.Platform, dmOnly,
			bullet, prefix, dmOnly,
			bullet, prefix, dmOnly,
			bullet, prefix,
		), nil)

	default:
		_ = b.Channel.SendResponse(msg, fmt.Sprintf(
			"Unknown command: `%s%s`. Use `%shelp` for available commands.", prefix, cmd, prefix), nil)
	}
}

// --- permission wiring ---

// RegisterPendingPerm stores perm in the session and, if it has a ReactionKey, in the global index.
func (b *BaseBridge) RegisterPendingPerm(sess *SessionConn, perm *PendingPermCore) {
	sess.PermMu.Lock()
	sess.PendingPerms[perm.RequestID] = perm
	sess.PermMu.Unlock()

	if perm.ReactionKey != "" {
		b.ReactionIndexMu.Lock()
		b.ReactionIndex[perm.ReactionKey] = sess.Key
		b.ReactionIndexMu.Unlock()
	}
}

// SpawnPermWaiter starts a goroutine that waits for a reaction/action response,
// then sends the agentd permission response. On timeout or stop it auto-denies.
func (b *BaseBridge) SpawnPermWaiter(sess *SessionConn, requestID, reactionKey string, responseChan <-chan bool) {
	shortID := requestID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	name := b.Platform.Name

	b.SpawnRecoverable("permWaiter:"+shortID, func() {
		approvalTimeout := b.Cfg.Permissions.ApprovalTimeout
		if approvalTimeout <= 0 {
			approvalTimeout = 60 * time.Second
		}

		cleanup := func() {
			sess.PermMu.Lock()
			delete(sess.PendingPerms, requestID)
			sess.PermMu.Unlock()
			if reactionKey != "" {
				b.ReactionIndexMu.Lock()
				delete(b.ReactionIndex, reactionKey)
				b.ReactionIndexMu.Unlock()
			}
		}

		var approved bool
		select {
		case approved = <-responseChan:
			logging.Infof("%s: permission %s resolved: approved=%v", name, shortID, approved)
		case <-time.After(approvalTimeout):
			logging.Warnf("%s: permission %s timed out after %s; denying", name, shortID, approvalTimeout)
			approved = false
			cleanup()
		case <-b.StopChan:
			approved = false
			cleanup()
		}

		if err := sess.Conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{
				RequestID: requestID,
				Approved:  approved,
			},
		}); err != nil {
			logging.Errorf("%s: send perm response: %v", name, err)
		}
	})
}

// --- eviction ---

// StartEvictionLoop starts a goroutine that periodically evicts idle sessions.
func (b *BaseBridge) StartEvictionLoop() {
	b.SpawnRecoverable("evictionLoop", func() {
		interval := b.Platform.SessionTTL / 4
		if interval < time.Minute {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-b.StopChan:
				return
			case <-ticker.C:
				b.evictIdleSessions()
			}
		}
	})
}

func (b *BaseBridge) evictIdleSessions() {
	cutoff := time.Now().Add(-b.Platform.SessionTTL)

	b.Mu.Lock()
	var evicted []string
	for key, sess := range b.Sessions {
		if sess.GetLastActivity().Before(cutoff) {
			_ = sess.Conn.Close()
			delete(b.Sessions, key)
			evicted = append(evicted, key)
		}
	}
	b.Mu.Unlock()

	if len(evicted) == 0 {
		return
	}

	logging.Infof("%s: evicted %d idle sessions: %v", b.Platform.Name, len(evicted), evicted)

	store, _ := LoadSessionStore(b.Platform.SessionFile)
	for _, key := range evicted {
		delete(store, key)
	}
	if err := SaveSessionStore(b.Platform.SessionFile, b.Platform.Name+"-", store); err != nil {
		logging.Warnf("%s: save session store after eviction: %v", b.Platform.Name, err)
	}
}
