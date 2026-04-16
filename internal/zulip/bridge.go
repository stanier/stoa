package zulip

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stanier/stoa/internal/logging"
	tea "github.com/charmbracelet/bubbletea"
)

// Bridge manages the connection to Zulip and event handling
type Bridge struct {
	client          *Client
	config          BridgeConfig
	connected       bool
	connectedMu     sync.RWMutex
	stopChan        chan struct{}
	wg              sync.WaitGroup
	msgChan         chan tea.Msg
	responseChan    chan MessageResponse
	reactionTracker *ReactionTracker
	botUserID       int
	botEmail        string
	botFullName     string
	lastPollTime    time.Time
	pollInterval    time.Duration
	ctx             context.Context
	cancelCtx       context.CancelFunc
}

// BridgeConfig holds configuration for the Zulip bridge
type BridgeConfig struct {
	SiteURL         string
	Email           string
	APIKey          string
	EventTypes      []string
	UseReactions    bool
	EmergencyCutoff string
	PollInterval    time.Duration // Minimum delay between polls (deprecated, use MinPollDelay)
	MinPollDelay    time.Duration // Minimum delay between successful polls
	// AllowedStreams is the set of stream names the bot will respond to freely.
	// Messages from streams not in this list are only processed when the bot is
	// @-mentioned. An empty list means all streams are allowed (default behaviour).
	AllowedStreams []string
}

// MessageResponse is sent when the LLM has a response to send back
type MessageResponse struct {
	OriginalMsg *Message
	Response    string
	Thinking    string
	Error       error
}

// ReactionTracker tracks reaction lifecycle per message
type ReactionTracker struct {
	mu        sync.RWMutex
	reactions map[int]string // messageID -> current emoji
}

// NewReactionTracker creates a new reaction tracker
func NewReactionTracker() *ReactionTracker {
	return &ReactionTracker{
		reactions: make(map[int]string),
	}
}

// SetReaction sets the current reaction on a message
func (rt *ReactionTracker) SetReaction(messageID int, emoji string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.reactions[messageID] = emoji
}

// GetReaction gets the current reaction on a message
func (rt *ReactionTracker) GetReaction(messageID int) string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.reactions[messageID]
}

// RemoveReaction removes a reaction from tracking
func (rt *ReactionTracker) RemoveReaction(messageID int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.reactions, messageID)
}

// NewBridge creates a new Zulip bridge
func NewBridge(config BridgeConfig, msgChan chan tea.Msg) *Bridge {
	// Backward compatibility: honour PollInterval if the newer MinPollDelay was not set.
	if config.PollInterval != 0 && config.MinPollDelay == 0 {
		config.MinPollDelay = config.PollInterval
	}

	// Apply default minimum poll delay when neither field was configured.
	if config.MinPollDelay == 0 {
		config.MinPollDelay = 1 * time.Second
	}

	return &Bridge{
		client:          NewClient(config.SiteURL, config.Email, config.APIKey),
		config:          config,
		stopChan:        make(chan struct{}),
		msgChan:         msgChan,
		responseChan:    make(chan MessageResponse, 10),
		reactionTracker: NewReactionTracker(),
		pollInterval:    config.MinPollDelay,
	}
}

// Start starts the Zulip bridge in a background goroutine
func (b *Bridge) Start(ctx context.Context) error {
	// Register event queue
	eventTypes := b.config.EventTypes
	hasReaction := false
	for _, et := range eventTypes {
		if et == "reaction" {
			hasReaction = true
			break
		}
	}
	if !hasReaction {
		eventTypes = append(eventTypes, "reaction")
	}

	// Fetch bot's own user ID and full name so we can filter self-reactions and detect mentions.
	ownUserID, ownFullName, err := b.client.GetOwnProfile(ctx)
	if err != nil {
		logging.Warnf("Failed to fetch bot profile (self-reactions may not be filtered, mention detection disabled): %v", err)
	} else {
		b.botUserID = ownUserID
		b.botFullName = ownFullName
		logging.Debugf("Bot user ID: %d, full name: %q", b.botUserID, b.botFullName)
	}

	queueInfo, err := b.client.RegisterQueue(ctx, eventTypes)
	if err != nil {
		return fmt.Errorf("failed to register queue: %w", err)
	}

	b.connectedMu.Lock()
	b.connected = true
	b.connectedMu.Unlock()

	// Create cancellable context for bridge lifecycle
	// This allows us to cancel in-flight HTTP requests when Stop() is called
	b.ctx, b.cancelCtx = context.WithCancel(context.Background())

	b.wg.Add(1)
	go b.eventLoop(b.ctx, queueInfo.QueueID)

	b.wg.Add(1)
	go b.responseHandler(b.ctx)

	return nil
}

// Stop stops the Zulip bridge
func (b *Bridge) Stop(ctx context.Context) error {
	// Cancel bridge context to abort in-flight HTTP requests
	if b.cancelCtx != nil {
		b.cancelCtx()
	}

	close(b.stopChan)

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for bridge goroutines")
	}

	b.connectedMu.Lock()
	b.connected = false
	b.connectedMu.Unlock()

	// Delete the queue
	if b.client.queueID != "" {
		_ = b.client.DeleteQueue(context.Background(), b.client.queueID)
	}

	return nil
}

// IsConnected returns whether the bridge is connected
func (b *Bridge) IsConnected() bool {
	b.connectedMu.RLock()
	defer b.connectedMu.RUnlock()
	return b.connected
}

// SendResponse sends a response back to Zulip
func (b *Bridge) SendResponse(originalMsg *Message, response string, err error) {
	logging.Debugf("bridge.SendResponse called for msg_id=%d (len=%d)", originalMsg.ID, len(response))
	select {
	case b.responseChan <- MessageResponse{
		OriginalMsg: originalMsg,
		Response:    response,
		Error:       err,
	}:
	case <-b.stopChan:
	}
}

// privateRecipient builds the Zulip "to" value for a private message reply.
// For group DMs all participants except the bot itself are included so the
// reply stays in the group thread rather than becoming a 1:1 DM.
func (b *Bridge) privateRecipient(msg *Message) string {
	var emails []string
	for _, r := range msg.DisplayRecipient.Recipients {
		if r.Email != b.config.Email {
			emails = append(emails, r.Email)
		}
	}
	if len(emails) > 0 {
		return strings.Join(emails, ",")
	}
	return msg.SenderEmail
}

// SendMessage sends a new message to Zulip and returns the message ID
func (b *Bridge) SendMessage(originalMsg *Message, content string) (int, error) {
	// Determine message type and recipient
	var msgType, to, topic string
	if originalMsg.Type == "stream" || originalMsg.Type == "channel" {
		msgType = "stream"
		to = originalMsg.DisplayRecipient.String()
		topic = originalMsg.Subject
	} else {
		msgType = "private"
		to = b.privateRecipient(originalMsg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return b.client.SendMessage(ctx, msgType, to, content, topic)
}

// AddReaction adds a reaction to a message
func (b *Bridge) AddReaction(messageID int, emoji string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return b.client.AddReaction(ctx, messageID, emoji)
}

// eventLoop polls for events from Zulip
func (b *Bridge) eventLoop(ctx context.Context, queueID string) {
	defer b.wg.Done()

	retryCount := 0
	maxRetries := 5
	backoffDuration := 1 * time.Second

	for {
		select {
		case <-b.stopChan:
			return
		default:
		}

		// Long-poll for events with server timeout (Zulip waits up to 30s for events)
		// Use a client timeout slightly longer than server timeout to avoid premature cancellation
		pollCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		events, err := b.client.GetEvents(pollCtx, queueID, b.client.lastEventID)
		cancel()
		if err != nil {
			// If parent context was cancelled, exit immediately (shutdown in progress)
			if ctx.Err() != nil {
				return
			}

			// Context deadline exceeded during long-polling is normal when idle
			// Treat it as "no events" rather than an error
			if pollCtx.Err() == context.DeadlineExceeded {
				// Reset retry count since this is not a real error
				retryCount = 0
				// Continue polling without logging
				continue
			}

			// Real error - increment retry counter
			retryCount++

			// Check if this is a rate limit error
			isRateLimit := strings.Contains(err.Error(), "rate limited")

			if isRateLimit {
				logging.Warnf("Zulip rate limit hit (retry %d/%d), backing off...", retryCount, maxRetries)
				// For rate limits, use longer backoff (start at 5s instead of 1s)
				waitTime := 5 * time.Second * time.Duration(1<<uint(retryCount-1))
				if waitTime > 60*time.Second {
					waitTime = 60 * time.Second // Cap at 1 minute
				}
				logging.Debugf("Waiting %v before retrying...", waitTime)
				time.Sleep(waitTime)
			} else {
				logging.Warnf("Zulip event poll error (retry %d/%d): %v", retryCount, maxRetries, err)

				if retryCount > maxRetries {
					b.connectedMu.Lock()
					b.connected = false
					b.connectedMu.Unlock()

					// Send error message
					b.msgChan <- ConnectionStatusMsg{
						Connected: false,
						Error:     err,
					}
					return
				}

				// Exponential backoff for other errors
				time.Sleep(backoffDuration * time.Duration(1<<uint(retryCount-1)))
			}
			continue
		}

		// Successful poll - reset retry count
		retryCount = 0
		if !b.IsConnected() {
			b.connectedMu.Lock()
			b.connected = true
			b.connectedMu.Unlock()
			b.msgChan <- ConnectionStatusMsg{
				Connected: true,
				Error:     nil,
			}
		}

		// Process events
		hadEvents := len(events) > 0
		for _, event := range events {
			if event.Type == "message" && event.Msg != nil {
				// Ignore self-messages
				if event.Msg.SenderEmail == b.config.Email {
					continue
				}

				// Check for emergency cutoff
				if b.config.EmergencyCutoff != "" && strings.Contains(event.Msg.Content, b.config.EmergencyCutoff) {
					continue
				}

				// For stream messages, apply allowlist + mention-trigger logic.
				// DMs (type "private") are always processed.
				if event.Msg.Type == "stream" || event.Msg.Type == "channel" {
					if !b.streamIsAllowed(event.Msg.DisplayRecipient.Stream) && !b.isBotMentioned(event.Msg.Content) {
						logging.Debugf("Ignoring stream message in %q (not in allowlist and bot not mentioned)", event.Msg.DisplayRecipient.Stream)
						continue
					}
				}

				// Add reaction (gear emoji to indicate processing)
				if b.config.UseReactions {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					if err := b.client.AddReaction(ctx, event.Msg.ID, "gear"); err != nil {
						logging.Warnf("Failed to add 'gear' reaction to message %d: %v", event.Msg.ID, err)
					} else {
						b.reactionTracker.SetReaction(event.Msg.ID, "gear")
					}
					cancel()
				}

				// Convert Zulip message to a format for the UI
				// Send message to UI as a command that returns the message
				b.msgChan <- MessageReceivedMsg{
					Message:     event.Msg,
					BridgeReply: b.responseChan,
				}
			} else if event.Type == "reaction" && event.Op == "add" {
				// Skip reactions from the bot itself (e.g. the check/x we add to permission requests)
				if b.botUserID != 0 && event.UserID == b.botUserID {
					logging.Debugf("Ignoring self-reaction %q on message %d", event.EmojiName, event.MessageID)
				} else {
					// Forward reaction events from other users
					b.msgChan <- ReactionReceivedMsg{
						MessageID: event.MessageID,
						UserID:    event.UserID,
						EmojiName: event.EmojiName,
					}
				}
			}

			// Update last event ID
			if event.ID > b.client.lastEventID {
				b.client.lastEventID = event.ID
			}
		}

		b.lastPollTime = time.Now()

		// Add a small delay between polls to avoid rate limiting
		// Even with long polling, we should pace our requests
		if !hadEvents {
			// No events received - wait longer before next poll
			time.Sleep(b.pollInterval * 2)
		} else {
			// Events received - shorter delay for responsiveness
			time.Sleep(b.pollInterval / 2)
		}
	}
}

// responseHandler handles responses from the UI
func (b *Bridge) responseHandler(ctx context.Context) {
	defer b.wg.Done()

	for {
		select {
		case <-b.stopChan:
			return
		case response := <-b.responseChan:
			if response.OriginalMsg == nil {
				continue
			}

			msgID := response.OriginalMsg.ID

			// Send response to Zulip
			if response.Error == nil {
				formattedResponse := b.formatResponseForZulip(response.Response, response.Thinking)

				// Determine message type and recipient
				var msgType, to, topic string
				if response.OriginalMsg.Type == "stream" || response.OriginalMsg.Type == "channel" {
					msgType = "stream"
					to = response.OriginalMsg.DisplayRecipient.String()
					topic = response.OriginalMsg.Subject
				} else {
					msgType = "private"
					to = b.privateRecipient(response.OriginalMsg)
				}

				logging.Debugf("Sending Zulip message for msg_id=%d (len=%d)", msgID, len(formattedResponse))
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_, err := b.client.SendMessage(ctx, msgType, to, formattedResponse, topic)
				cancel()

				if err != nil {
					// Log error but continue
					logging.Errorf("Error sending message to Zulip: %v", err)
				}
			}

			// Remove "gear" reaction after sending (keep "wrench" if present)
			if b.config.UseReactions {
				emoji := b.reactionTracker.GetReaction(msgID)
				if emoji == "gear" {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					if err := b.client.RemoveReaction(ctx, msgID, emoji); err != nil {
						logging.Warnf("Failed to remove 'gear' reaction from message %d: %v", msgID, err)
					}
					cancel()
					b.reactionTracker.RemoveReaction(msgID)
				}
				// Note: "wrench" reaction is kept as a persistent indicator of tool usage
			}
		}
	}
}

// streamIsAllowed reports whether the bot should auto-respond in the given stream.
// When AllowedStreams is empty, all streams are allowed (backward compatible).
func (b *Bridge) streamIsAllowed(stream string) bool {
	if len(b.config.AllowedStreams) == 0 {
		return true
	}
	for _, s := range b.config.AllowedStreams {
		if strings.EqualFold(s, stream) {
			return true
		}
	}
	return false
}

// isBotMentioned reports whether content contains an @-mention of the bot.
// Zulip represents mentions as @**Full Name** or @_**Full Name|ID**.
func (b *Bridge) isBotMentioned(content string) bool {
	if b.botFullName != "" && strings.Contains(content, "@**"+b.botFullName+"**") {
		return true
	}
	if b.botUserID != 0 && strings.Contains(content, fmt.Sprintf("|%d**", b.botUserID)) {
		return true
	}
	return false
}

// formatResponseForZulip converts our response format to Zulip format
// formatResponseForZulip wraps thinking in a Zulip spoiler block when present.
func (b *Bridge) formatResponseForZulip(response, thinking string) string {
	if thinking != "" {
		return fmt.Sprintf("```spoiler Thinking\n%s\n```\n\n%s", strings.TrimSpace(thinking), strings.TrimSpace(response))
	}
	return response
}

// MessageReceivedMsg is sent by the bridge when a message is received
type MessageReceivedMsg struct {
	Message     *Message
	BridgeReply chan<- MessageResponse
}

// ReactionReceivedMsg is sent by the bridge when a reaction is added
type ReactionReceivedMsg struct {
	MessageID int
	UserID    int
	EmojiName string
}

// ConnectionStatusMsg is sent by the bridge when connection status changes
type ConnectionStatusMsg struct {
	Connected bool
	Error     error
}
