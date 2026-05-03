package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridges"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/slack"
)

// mentionRe strips Slack @-mention markup (<@UXXXXXXXX>).
var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>`)

func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

func bold(s string) string { return "*" + s + "*" }

// Bridge connects Slack to agentd.
type Bridge struct {
	*bridges.BaseBridge
	channel *channels.SlackChannel

	// emailCache maps Slack user ID → email address; populated lazily via Slack API.
	emailCache   map[string]string
	emailCacheMu sync.Mutex
}

func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.SlackChannel) *Bridge {
	idleTTL := cfg.Slack.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	unauth := cfg.Slack.AccessPolicy.Unauthenticated
	platform := bridges.PlatformConfig{
		Name:            "slack-bridge",
		Source:          "slack",
		Platform:        "Slack",
		StatusPath:      "/link/slack/status",
		StatusParam:     "email",
		StartPath:       "/link/slack/start",
		LinkBodyKey:     "slack_email",
		ResolvePath:     "/link/slack/resolve",
		UnlinkPath:      "/link/slack/unlink",
		DmType:          "direct",
		ShowIdentifier:  true,
		DefaultPersona:  cfg.Slack.Persona,
		UnauthPolicy:    bridges.RolePolicy{Allow: unauth.Allow, Persona: unauth.Persona, AllowedTools: unauth.AllowedTools},
		ApprovalTimeout: cfg.Permissions.ApprovalTimeout,
		CommandPrefix:   cfg.Slack.CommandPrefix,
		SessionTTL:      idleTTL,
		SessionFile:     cfg.Slack.SessionFile,
		Bold:            bold,
		Bullet:          "• ",
	}
	return &Bridge{
		BaseBridge: bridges.NewBaseBridge(cfg, platform, channel, agentClient),
		channel:    channel,
		emailCache: make(map[string]string),
	}
}

// conversationKey returns a stable session key for a Slack message's conversation.
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
	ts := msg.ID
	if raw != nil {
		ts = raw.Timestamp
	}
	return "channel:" + msg.Recipient + ":thread:" + ts
}

func reactionKey(channelID, messageTS string) string { return channelID + ":" + messageTS }

// resolveEmail returns the email for a Slack user ID, using cache then Slack API.
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

func (b *Bridge) Start(_ context.Context) error {
	b.SpawnRecoverable("messageHandler", func() { b.messageHandler() })
	b.SpawnRecoverable("reactionHandler", func() { b.reactionHandler() })
	b.SpawnRecoverable("blockActionHandler", func() { b.blockActionHandler() })
	b.StartEvictionLoop()
	return nil
}

func (b *Bridge) messageHandler() {
	commandPrefix := b.Platform.CommandPrefix
	if commandPrefix == "" {
		commandPrefix = "!"
	}

	for {
		select {
		case <-b.StopChan:
			return

		case msg, ok := <-b.channel.Messages():
			if !ok {
				return
			}
			logging.Infof("slack-bridge: received message from %s (len=%d, type=%s)",
				msg.SenderID, len(msg.Content), msg.Type)

			// Ensure root channel messages start a thread.
			if msg.Type == "channel" {
				if raw, ok := msg.Raw.(*slack.MessageEvent); ok && raw.ThreadTimestamp == "" {
					raw.ThreadTimestamp = raw.Timestamp
				}
			}

			key := conversationKey(msg)
			trimmed := strings.TrimSpace(msg.Content)
			email := b.resolveEmail(context.Background(), msg.SenderID)

			if strings.HasPrefix(trimmed, commandPrefix) {
				fields := strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))
				if len(fields) > 0 {
					switch strings.ToLower(fields[0]) {
					case "link":
						b.HandleAuthCommand(context.Background(), msg, email)
						continue
					case "unlink":
						b.HandleUnlinkCommand(context.Background(), msg, email)
						// Also clear the local email cache entry.
						b.emailCacheMu.Lock()
						delete(b.emailCache, msg.SenderID)
						b.emailCacheMu.Unlock()
						continue
					case "status", "whoami":
						b.HandleStatusCommand(context.Background(), msg, email)
						continue
					}
				}
			}

			policy := b.ResolveAccessPolicy(context.Background(), email)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.Platform.DefaultPersona
			}

			agentUserID := b.ResolveUserID(context.Background(), email)

			sess, err := b.GetOrCreateSession(context.Background(), key, effectivePersona, agentUserID, policy.AllowedTools,
				func(s *bridges.SessionConn) { b.sessionEventHandler(s) })
			if err != nil {
				logging.Errorf("slack-bridge: get/create session for %q: %v", key, err)
				_ = b.channel.SendResponse(msg, "Error connecting to agent: "+err.Error(), err)
				continue
			}

			sess.CurrentMsgMu.Lock()
			msgCopy := msg
			sess.CurrentMsg = &msgCopy
			sess.CurrentMsgMu.Unlock()

			if strings.HasPrefix(trimmed, commandPrefix) {
				b.HandleCommand(sess, msgCopy, strings.TrimPrefix(trimmed, commandPrefix), commandPrefix)
				continue
			}

			content := stripMentions(msg.Content)
			if err := sess.Conn.Send(agentproto.ClientMessage{
				Type:        agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{Content: content},
			}); err != nil {
				logging.Errorf("slack-bridge: send to agentd session %s: %v", sess.SessionID[:8], err)
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

func (b *Bridge) reactionHandler() {
	reactionCh := b.channel.Reactions()
	for {
		select {
		case <-b.StopChan:
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
	if emoji != "white_check_mark" && emoji != "heavy_check_mark" && emoji != "x" {
		return
	}
	approved := emoji != "x"

	rk := reactionKey(reaction.ChannelID, reaction.MessageTS)
	b.ReactionIndexMu.Lock()
	key, found := b.ReactionIndex[rk]
	b.ReactionIndexMu.Unlock()
	if !found {
		return
	}

	b.Mu.RLock()
	sess := b.Sessions[key]
	b.Mu.RUnlock()
	if sess == nil {
		return
	}

	sess.PermMu.Lock()
	defer sess.PermMu.Unlock()

	var matched *bridges.PendingPermCore
	var matchedID string
	for id, p := range sess.PendingPerms {
		if p.ReactionKey == rk {
			matched = p
			matchedID = id
			break
		}
	}
	if matched == nil {
		return
	}

	select {
	case matched.ResponseChan <- approved:
		if approved {
			logging.Infof("slack-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("slack-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.PendingPerms, matchedID)
		b.ReactionIndexMu.Lock()
		delete(b.ReactionIndex, rk)
		b.ReactionIndexMu.Unlock()
	default:
		logging.Debugf("slack-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
	}
}

func (b *Bridge) blockActionHandler() {
	blockActionCh := b.channel.BlockActions()
	for {
		select {
		case <-b.StopChan:
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

	rk := reactionKey(action.ChannelID, action.MessageTS)
	b.ReactionIndexMu.Lock()
	key, found := b.ReactionIndex[rk]
	b.ReactionIndexMu.Unlock()
	if !found {
		return
	}

	b.Mu.RLock()
	sess := b.Sessions[key]
	b.Mu.RUnlock()
	if sess == nil {
		return
	}

	sess.PermMu.Lock()
	defer sess.PermMu.Unlock()

	var matched *bridges.PendingPermCore
	var matchedID string
	for id, p := range sess.PendingPerms {
		if p.ReactionKey == rk {
			matched = p
			matchedID = id
			break
		}
	}
	if matched == nil {
		return
	}

	select {
	case matched.ResponseChan <- approved:
		if approved {
			logging.Infof("slack-bridge: permission %s approved via block action", matchedID[:8])
		} else {
			logging.Infof("slack-bridge: permission %s denied via block action", matchedID[:8])
		}
		delete(sess.PendingPerms, matchedID)
		b.ReactionIndexMu.Lock()
		delete(b.ReactionIndex, rk)
		b.ReactionIndexMu.Unlock()
	default:
		logging.Debugf("slack-bridge: duplicate block action for permission %s, ignoring", matchedID[:8])
	}
}

func (b *Bridge) sessionEventHandler(sess *bridges.SessionConn) {
	events := sess.Conn.Events()
	var accumContent strings.Builder

	for {
		select {
		case <-b.StopChan:
			return
		case e, ok := <-events:
			if !ok {
				logging.Infof("slack-bridge: agentd connection closed for session %s (key=%q)",
					sess.SessionID[:8], sess.Key)
				b.RemoveSession(sess.Key)
				return
			}
			b.handleEvent(sess, e, &accumContent)
		}
	}
}

func (b *Bridge) handleEvent(sess *bridges.SessionConn, e agentproto.Event, accumContent *strings.Builder) {
	sess.CurrentMsgMu.RLock()
	currentMsg := sess.CurrentMsg
	sess.CurrentMsgMu.RUnlock()

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
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg,
				fmt.Sprintf("```\nTool invoked: %s\n%s\n```", e.ToolInvoked.ToolName, paramsStr), nil)
		}

	case agentproto.EventTypeToolResult:
		// Informational; agentd handles internally.

	case agentproto.EventTypePermRequest:
		if e.PermRequest != nil {
			b.handlePermRequest(sess, e.PermRequest, currentMsg)
		}

	case agentproto.EventTypePermMention:
		if e.PermMention != nil && currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg,
				fmt.Sprintf("_Tool `%s` ran automatically: %s_", e.PermMention.ToolName, e.PermMention.Description), nil)
		}

	case agentproto.EventTypeCompaction:
		if e.Compaction != nil && currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg,
				fmt.Sprintf("_Conversation history compacted (%d → %d messages)._",
					e.Compaction.MessagesBefore, e.Compaction.MessagesAfter), nil)
		}

	case agentproto.EventTypeError:
		if e.Error == nil {
			return
		}
		if currentMsg != nil {
			_ = b.channel.SendResponse(*currentMsg, "Error: "+e.Error.Message,
				fmt.Errorf("%s", e.Error.Message))
		}
		if e.Error.Fatal {
			logging.Errorf("slack-bridge: fatal agentd error on session %s: %s", sess.SessionID[:8], e.Error.Message)
		}
	}
}

func (b *Bridge) handlePermRequest(sess *bridges.SessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("slack-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
		_ = sess.Conn.Send(agentproto.ClientMessage{
			Type: agentproto.MessageTypePermResponse,
			PermResponse: &agentproto.PermResponsePayload{RequestID: req.RequestID, Approved: false},
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
		req.ToolName, req.Description, paramsJSON)

	blocks := []map[string]any{
		{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": sectionText}},
		{"type": "actions", "elements": []map[string]any{
			{"type": "button", "action_id": "perm_approve", "value": "approve",
				"text": map[string]any{"type": "plain_text", "text": "Approve"}},
			{"type": "button", "action_id": "perm_deny", "value": "deny",
				"text": map[string]any{"type": "plain_text", "text": "Deny"}},
		}},
	}

	msgTS, err := b.channel.SendBlocksMessageWithID(*originalMsg, blocks)
	if err != nil {
		logging.Warnf("slack-bridge: send permission block kit message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	raw, _ := originalMsg.Raw.(*slack.MessageEvent)
	channelID := ""
	if raw != nil {
		channelID = raw.ChannelID
	}

	rk := reactionKey(channelID, msgTS)
	responseChan := make(chan bool, 1)
	b.RegisterPendingPerm(sess, &bridges.PendingPermCore{
		RequestID:    req.RequestID,
		ReactionKey:  rk,
		OriginalMsg:  originalMsg,
		ResponseChan: responseChan,
		RequestedAt:  time.Now(),
	})
	b.SpawnPermWaiter(sess, req.RequestID, rk, responseChan)
}
