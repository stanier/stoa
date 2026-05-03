package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridges"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/matrix"
)

func bold(s string) string { return "**" + s + "**" }

// Bridge connects Matrix to agentd.
type Bridge struct {
	*bridges.BaseBridge
	channel *channels.MatrixChannel
}

func NewBridge(cfg *config.Config, agentClient agentproto.Client, channel *channels.MatrixChannel) *Bridge {
	idleTTL := cfg.Matrix.SessionTTL
	if idleTTL <= 0 {
		idleTTL = 24 * time.Hour
	}
	unauth := cfg.Matrix.AccessPolicy.Unauthenticated
	platform := bridges.PlatformConfig{
		Name:            "matrix-bridge",
		Source:          "matrix",
		Platform:        "Matrix",
		StatusPath:      "/link/matrix/status",
		StatusParam:     "matrix_user_id",
		StartPath:       "/link/matrix/start",
		LinkBodyKey:     "matrix_user_id",
		ResolvePath:     "/link/matrix/resolve",
		UnlinkPath:      "/link/matrix/unlink",
		DmType:          "", // no DM restriction for Matrix
		ShowIdentifier:  false,
		DefaultPersona:  cfg.Matrix.Persona,
		UnauthPolicy:    bridges.RolePolicy{Allow: unauth.Allow, Persona: unauth.Persona, AllowedTools: unauth.AllowedTools},
		ApprovalTimeout: cfg.Permissions.ApprovalTimeout,
		CommandPrefix:   cfg.Matrix.CommandPrefix,
		SessionTTL:      idleTTL,
		SessionFile:     cfg.Matrix.SessionFile,
		Bold:            bold,
		Bullet:          "- ",
	}
	return &Bridge{
		BaseBridge: bridges.NewBaseBridge(cfg, platform, channel, agentClient),
		channel:    channel,
	}
}

func reactionKey(roomID, eventID string) string { return roomID + ":" + eventID }

// conversationKey returns the session routing key for a Matrix message.
//
//	DM:     "dm:<matrix_user_id>"
//	Thread: "thread:<thread_root_event_id>"
func conversationKey(msg channels.Message) string {
	if msg.Type == "direct" {
		return "dm:" + msg.SenderID
	}
	return "thread:" + msg.Topic
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
			logging.Infof("matrix-bridge: received message from %s (len=%d)", msg.SenderID, len(msg.Content))

			// Determine session key and thread context.
			var key string
			var threadRootID string
			if msg.Type == "direct" {
				key = "dm:" + msg.SenderID
			} else {
				if msg.Topic != "" {
					threadRootID = msg.Topic
				} else {
					threadRootID = msg.ID
				}
				key = "thread:" + threadRootID
			}
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
				logging.Errorf("matrix-bridge: get/create session for %q: %v", key, err)
				_ = b.channel.SendResponse(msg, "Error connecting to agent: "+err.Error(), err)
				continue
			}

			// Record thread/room context for non-DM sessions so replies are threaded.
			if threadRootID != "" {
				sess.ThreadRootID = threadRootID
				sess.RoomID = msg.Recipient
			}

			sess.CurrentMsgMu.Lock()
			msgCopy := msg
			sess.CurrentMsg = &msgCopy
			sess.CurrentMsgMu.Unlock()

			if strings.HasPrefix(trimmed, commandPrefix) {
				b.HandleCommand(sess, msgCopy, strings.TrimPrefix(trimmed, commandPrefix), commandPrefix)
				continue
			}

			if err := sess.Conn.Send(agentproto.ClientMessage{
				Type:        agentproto.MessageTypeSendMessage,
				SendMessage: &agentproto.SendMessagePayload{Content: trimmed},
			}); err != nil {
				logging.Errorf("matrix-bridge: send to agentd session %s: %v", sess.SessionID[:8], err)
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

func (b *Bridge) handleReaction(reaction matrix.ReactionEvent) {
	emoji := reaction.Emoji
	if emoji != "✅" && emoji != "❌" {
		return
	}
	approved := emoji == "✅"

	rk := reactionKey(reaction.RoomID, reaction.TargetEvent)
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
			logging.Infof("matrix-bridge: permission %s approved via reaction", matchedID[:8])
		} else {
			logging.Infof("matrix-bridge: permission %s denied via reaction", matchedID[:8])
		}
		delete(sess.PendingPerms, matchedID)
		b.ReactionIndexMu.Lock()
		delete(b.ReactionIndex, rk)
		b.ReactionIndexMu.Unlock()
	default:
		logging.Debugf("matrix-bridge: duplicate reaction for permission %s, ignoring", matchedID[:8])
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
				logging.Infof("matrix-bridge: agentd connection closed for session %s (key=%q)",
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
			if sess.ThreadRootID != "" && sess.RoomID != "" {
				if _, err := b.channel.SendThreadedMessage(sess.RoomID, sess.ThreadRootID, content); err != nil {
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
		if currentMsg != nil {
			plain := "Tool invoked: " + e.ToolInvoked.ToolName + "\n" + paramsStr
			html := "<strong>Tool invoked:</strong> <code>" + e.ToolInvoked.ToolName +
				"</code><br><pre><code>" + paramsStr + "</code></pre>"
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
		}

	case agentproto.EventTypeToolResult:
		// Informational; agentd handles internally.

	case agentproto.EventTypePermRequest:
		if e.PermRequest != nil {
			b.handlePermRequest(sess, e.PermRequest, currentMsg)
		}

	case agentproto.EventTypePermMention:
		if e.PermMention != nil && currentMsg != nil {
			plain := "Tool " + e.PermMention.ToolName + " ran automatically: " + e.PermMention.Description
			html := "<strong>Tool <code>" + e.PermMention.ToolName +
				"</code> ran automatically:</strong> " + e.PermMention.Description
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
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
			plain := "Error: " + e.Error.Message
			html := "<strong>Error:</strong> " + e.Error.Message
			_, _ = b.channel.SendFormattedMessage(*currentMsg, plain, html)
		}
		if e.Error.Fatal {
			logging.Errorf("matrix-bridge: fatal agentd error on session %s: %s", sess.SessionID[:8], e.Error.Message)
		}
	}
}

func (b *Bridge) handlePermRequest(sess *bridges.SessionConn, req *agentproto.PermRequestEvent, originalMsg *channels.Message) {
	autoDeny := func(reason string) {
		logging.Warnf("matrix-bridge: permission request for %s — %s; auto-denying", req.ToolName, reason)
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

	plain := fmt.Sprintf(
		"Permission request: %s\n%s\nParameters:\n%s\nReact with ✅ to approve or ❌ to deny. Times out in 60 seconds.",
		req.ToolName, req.Description, paramsJSON)
	html := fmt.Sprintf(
		"<strong>Permission request:</strong> <code>%s</code><br>"+
			"<strong>Description:</strong> %s<br>"+
			"<strong>Parameters:</strong><br><pre><code>%s</code></pre>"+
			"React with ✅ to approve or ❌ to deny. Times out in 60 seconds.",
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

	rk := reactionKey(roomID, eventID)
	if roomID != "" && eventID != "" {
		if err := b.channel.AddReactionByID(roomID, eventID, "✅"); err != nil {
			logging.Warnf("matrix-bridge: add check reaction: %v", err)
		}
		if err := b.channel.AddReactionByID(roomID, eventID, "❌"); err != nil {
			logging.Warnf("matrix-bridge: add x reaction: %v", err)
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
