package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	dg "github.com/bwmarrin/discordgo"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridges"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/discord"
	"github.com/stanier/stoa/internal/logging"
)

// mentionRe strips Discord @-mention markup (<@SNOWFLAKE> and <@!SNOWFLAKE>).
var mentionRe = regexp.MustCompile(`<@!?[0-9]+>`)

func stripMentions(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllLiteralString(content, ""))
}

func bold(s string) string { return "**" + s + "**" }

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

// Bridge connects Discord to agentd.
type Bridge struct {
	*bridges.BaseBridge
	channel *channels.DiscordChannel
}

func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.DiscordChannel) *Bridge {
	idleTTL := cfg.Discord.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	unauth := cfg.Discord.AccessPolicy.Unauthenticated
	platform := bridges.PlatformConfig{
		Name:            "discord-bridge",
		Source:          "discord",
		Platform:        "Discord",
		StatusPath:      "/link/discord/status",
		StatusParam:     "discord_user_id",
		StartPath:       "/link/discord/start",
		LinkBodyKey:     "discord_user_id",
		ResolvePath:     "/link/discord/resolve",
		UnlinkPath:      "/link/discord/unlink",
		DmType:          "", // no DM restriction for Discord
		ShowIdentifier:  false,
		DefaultPersona:  cfg.Discord.Persona,
		UnauthPolicy:    bridges.RolePolicy{Allow: unauth.Allow, Persona: unauth.Persona, AllowedTools: unauth.AllowedTools},
		ApprovalTimeout: cfg.Permissions.ApprovalTimeout,
		CommandPrefix:   cfg.Discord.CommandPrefix,
		SessionTTL:      idleTTL,
		SessionFile:     cfg.Discord.SessionFile,
		Bold:            bold,
		Bullet:          "- ",
	}
	return &Bridge{
		BaseBridge: bridges.NewBaseBridge(cfg, platform, channel, agentClient),
		channel:    channel,
	}
}

func reactionKey(channelID, messageID string) string { return channelID + ":" + messageID }

// conversationKey returns a stable session key for a Discord message.
//
//	DM:     "dm:<discord_user_id>"
//	Thread: "thread:<thread_channel_id>"
func conversationKey(msg channels.Message) string {
	if msg.Type == "thread" {
		return "thread:" + msg.Topic
	}
	return "dm:" + msg.SenderID
}

func (b *Bridge) Start(_ context.Context) error {
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
			logging.Infof("discord-bridge: received message from %s type=%s (len=%d)",
				msg.SenderID, msg.Type, len(msg.Content))

			if msg.Type != "direct" && msg.Type != "thread" && msg.Type != "guild" {
				continue
			}

			// For guild channel messages, create a thread and re-route into it.
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

			if strings.HasPrefix(trimmed, commandPrefix) {
				fields := strings.Fields(strings.TrimPrefix(trimmed, commandPrefix))
				if len(fields) > 0 {
					switch strings.ToLower(fields[0]) {
					case "link":
						b.HandleAuthCommand(context.Background(), msg, msg.SenderID)
						continue
					case "unlink":
						b.HandleUnlinkCommand(context.Background(), msg, msg.SenderID)
						continue
					case "status", "whoami":
						b.HandleStatusCommand(context.Background(), msg, msg.SenderID)
						continue
					}
				}
			}

			policy := b.ResolveAccessPolicy(context.Background(), msg.SenderID)
			if !policy.Allow {
				_ = b.channel.SendResponse(msg,
					"You are not authorized to use this bot. Please link your account with `!link`.", nil)
				continue
			}

			effectivePersona := policy.Persona
			if effectivePersona == "" {
				effectivePersona = b.Platform.DefaultPersona
			}

			agentUserID := b.ResolveUserID(context.Background(), msg.SenderID)

			sess, err := b.GetOrCreateSession(context.Background(), key, effectivePersona, agentUserID, policy.AllowedTools,
				func(s *bridges.SessionConn) { b.sessionEventHandler(s) })
			if err != nil {
				logging.Errorf("discord-bridge: get/create session for %q: %v", key, err)
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
				logging.Errorf("discord-bridge: send to agentd session %s: %v", sess.SessionID[:8], err)
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

func (b *Bridge) handleReaction(reaction discord.ReactionEvent) {
	emoji := reaction.Emoji
	if emoji != "✅" && emoji != "white_check_mark" && emoji != "heavy_check_mark" && emoji != "❌" && emoji != "x" {
		return
	}
	approved := emoji != "❌" && emoji != "x"

	rk := reactionKey(reaction.ChannelID, reaction.MessageID)
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
			logging.Infof("discord-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("discord-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.PendingPerms, matchedID)
		b.ReactionIndexMu.Lock()
		delete(b.ReactionIndex, rk)
		b.ReactionIndexMu.Unlock()
	default:
		logging.Debugf("discord-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
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
				logging.Infof("discord-bridge: agentd connection closed for session %s (key=%q)",
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
		if currentMsg != nil {
			_, _ = b.channel.SendEmbed(*currentMsg, &dg.MessageEmbed{
				Title:       "Tool Invoked: " + e.ToolInvoked.ToolName,
				Description: "```json\n" + paramsStr + "\n```",
				Color:       0x5865F2,
			})
		}

	case agentproto.EventTypeToolResult:
		// Informational; agentd handles internally.

	case agentproto.EventTypePermRequest:
		if e.PermRequest != nil {
			b.handlePermRequest(sess, e.PermRequest, currentMsg)
		}

	case agentproto.EventTypePermMention:
		if e.PermMention != nil && currentMsg != nil {
			_, _ = b.channel.SendEmbed(*currentMsg, &dg.MessageEmbed{
				Title:       "Tool ran automatically: " + e.PermMention.ToolName,
				Description: e.PermMention.Description,
				Color:       0x57F287,
			})
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
			_, _ = b.channel.SendEmbed(*currentMsg, &dg.MessageEmbed{
				Title:       "Error",
				Description: e.Error.Message,
				Color:       0xED4245,
			})
		}
		if e.Error.Fatal {
			logging.Errorf("discord-bridge: fatal agentd error on session %s: %s", sess.SessionID[:8], e.Error.Message)
		}
	}
}

func (b *Bridge) handlePermRequest(sess *bridges.SessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("discord-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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

	embed := &dg.MessageEmbed{
		Title:       "Permission Request: " + req.ToolName,
		Description: req.Description,
		Fields: []*dg.MessageEmbedField{
			{Name: "Parameters", Value: "```json\n" + paramsJSON + "\n```", Inline: false},
			{Name: "Instructions", Value: "React with ✅ to approve or ❌ to deny. Times out in 60 seconds.", Inline: false},
		},
		Color: 0xFEE75C,
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

	rk := reactionKey(channelID, msgID)
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
	b.RegisterPendingPerm(sess, &bridges.PendingPermCore{
		RequestID:    req.RequestID,
		ReactionKey:  rk,
		OriginalMsg:  originalMsg,
		ResponseChan: responseChan,
		RequestedAt:  time.Now(),
	})
	b.SpawnPermWaiter(sess, req.RequestID, rk, responseChan)
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

// sendSplitResponse splits long content into Discord-safe chunks (≤2000 chars).
func (b *Bridge) sendSplitResponse(msg channels.Message, content string) {
	chunks := discord.SplitMessage(content, discord.DefaultMessageLimit)
	raw, isDiscord := msg.Raw.(*discord.MessageEvent)
	useThread := isDiscord && raw.IsThread && raw.ThreadID != ""

	for i, chunk := range chunks {
		if useThread {
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
