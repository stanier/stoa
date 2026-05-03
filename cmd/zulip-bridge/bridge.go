package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridges"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/zulip"
)

// mentionRe matches Zulip @-mention markup (@**Name** and @_**Name|id**).
var mentionRe = regexp.MustCompile(`@_?\*\*[^*]+\*\*`)

func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

func bold(s string) string { return "**" + s + "**" }

// Bridge connects Zulip to agentd.
type Bridge struct {
	*bridges.BaseBridge
	channel *channels.ZulipChannel
}

func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.ZulipChannel) *Bridge {
	idleTTL := cfg.Zulip.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	unauth := cfg.Zulip.AccessPolicy.Unauthenticated
	platform := bridges.PlatformConfig{
		Name:            "zulip-bridge",
		Source:          "zulip",
		Platform:        "Zulip",
		StatusPath:      "/link/zulip/status",
		StatusParam:     "email",
		StartPath:       "/link/zulip/start",
		LinkBodyKey:     "zulip_email",
		ResolvePath:     "/link/zulip/resolve",
		UnlinkPath:      "/link/zulip/unlink",
		DmType:          "private",
		ShowIdentifier:  true,
		DefaultPersona:  cfg.Zulip.Persona,
		UnauthPolicy:    bridges.RolePolicy{Allow: unauth.Allow, Persona: unauth.Persona, AllowedTools: unauth.AllowedTools},
		ApprovalTimeout: cfg.Permissions.ApprovalTimeout,
		CommandPrefix:   cfg.Zulip.CommandPrefix,
		SessionTTL:      idleTTL,
		SessionFile:     cfg.Zulip.SessionFile,
		Bold:            bold,
		Bullet:          "- ",
	}
	return &Bridge{
		BaseBridge: bridges.NewBaseBridge(cfg, platform, channel, agentClient),
		channel:    channel,
	}
}

// conversationKey maps a Zulip message to a stable string key.
//
//	Stream message: "stream:<stream_name>:<topic>"
//	DM:             "dm:<sorted_comma_separated_user_ids>"
func conversationKey(msg channels.Message) string {
	if msg.Type == "stream" {
		return "stream:" + msg.Recipient + ":" + msg.Topic
	}
	if zm, ok := msg.Raw.(*zulip.Message); ok {
		ids := make([]string, 0, len(zm.DisplayRecipient.Recipients))
		for _, r := range zm.DisplayRecipient.Recipients {
			ids = append(ids, strconv.Itoa(r.ID))
		}
		sort.Strings(ids)
		return "dm:" + strings.Join(ids, ",")
	}
	return "dm:" + msg.SenderID
}

func (b *Bridge) Start(ctx context.Context) error {
	b.SpawnRecoverable("messageHandler", func() { b.messageHandler() })
	b.SpawnRecoverable("reactionHandler", func() { b.reactionHandler() })
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
			logging.Infof("zulip-bridge: received message from %s (len=%d)", msg.SenderName, len(msg.Content))
			key := conversationKey(msg)
			trimmed := strings.TrimSpace(msg.Content)

			if strings.HasPrefix(trimmed, commandPrefix) {
				fields := strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))
				if len(fields) > 0 {
					switch strings.ToLower(fields[0]) {
					case "link":
						b.HandleAuthCommand(context.Background(), msg, msg.SenderEmail)
						continue
					case "unlink":
						b.HandleUnlinkCommand(context.Background(), msg, msg.SenderEmail)
						continue
					case "status", "whoami":
						b.HandleStatusCommand(context.Background(), msg, msg.SenderEmail)
						continue
					}
				}
			}

			policy := b.ResolveAccessPolicy(context.Background(), msg.SenderEmail)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.Platform.DefaultPersona
			}

			userID := b.ResolveUserID(context.Background(), msg.SenderEmail)

			sess, err := b.GetOrCreateSession(context.Background(), key, effectivePersona, userID, policy.AllowedTools,
				func(s *bridges.SessionConn) { b.sessionEventHandler(s) })
			if err != nil {
				logging.Errorf("zulip-bridge: get/create session for %q: %v", key, err)
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
			if len(msg.Attachments) > 0 {
				var lines []string
				for _, a := range msg.Attachments {
					lines = append(lines, fmt.Sprintf("- %s (path_id: %s, size: %.1f KB)",
						a.Name, a.PathID, float64(a.Size)/1024))
				}
				content = "[Attachments — use mcp_zulip-mcp_zulip_download_attachment to fetch them]\n" +
					strings.Join(lines, "\n") + "\n\n" + content
			}
			if err := sess.Conn.Send(agentproto.ClientMessage{
				Type:        agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{Content: content},
			}); err != nil {
				logging.Errorf("zulip-bridge: send to agentd session %s: %v", sess.SessionID[:8], err)
			}

		case status := <-b.channel.StatusUpdates():
			if status.Connected {
				logging.Info("zulip-bridge: Zulip connected")
			} else {
				logging.Errorf("zulip-bridge: Zulip disconnected: %v", status.Error)
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

func (b *Bridge) handleReaction(reaction zulip.ReactionReceivedMsg) {
	emojiName := reaction.EmojiName
	if emojiName != "check" && emojiName != "x" && emojiName != "cross_mark" {
		return
	}
	approved := emojiName == "check"

	rk := strconv.Itoa(reaction.MessageID)
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
			logging.Infof("zulip-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("zulip-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.PendingPerms, matchedID)
		b.ReactionIndexMu.Lock()
		delete(b.ReactionIndex, rk)
		b.ReactionIndexMu.Unlock()
	default:
		logging.Debugf("zulip-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
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
				logging.Infof("zulip-bridge: agentd connection closed for session %s (key=%q)",
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
			content = fmt.Sprintf("```spoiler Thinking\n%s\n```\n\n%s",
				strings.TrimSpace(thinking), strings.TrimSpace(content))
		}
		if currentMsg != nil {
			if err := b.channel.SendResponse(*currentMsg, content, nil); err != nil {
				logging.Errorf("zulip-bridge: send response: %v", err)
			}
		} else {
			logging.Warnf("zulip-bridge: DoneEvent but no current message context for session %s", sess.SessionID[:8])
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
				fmt.Sprintf("```spoiler Tool invoked: %s\n%s\n```", e.ToolInvoked.ToolName, paramsStr), nil)
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
			logging.Errorf("zulip-bridge: fatal agentd error on session %s: %s", sess.SessionID[:8], e.Error.Message)
		}
	}
}

func (b *Bridge) handlePermRequest(sess *bridges.SessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("zulip-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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
	permMsg := fmt.Sprintf(
		"Assistant is requesting permission to employ tool `%s`.  Please review the details below.\n"+
			"```spoiler Request Details\nTool's function is described as:\n````quote\n%s\n````\n\n"+
			"### Parameters\n````\n%s\n````\n---\n"+
			"Please use a reaction to signal authorization intent.  Request will timeout after 60 seconds.\n```",
		req.ToolName, req.Description, paramsJSON)

	msgID, err := b.channel.SendMessageWithID(*originalMsg, permMsg)
	if err != nil {
		logging.Warnf("zulip-bridge: send permission request message: %v", err)
		autoDeny("failed to send permission message")
		return
	}

	rk := strconv.Itoa(msgID)
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
