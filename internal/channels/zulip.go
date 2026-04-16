package channels

import (
	"context"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/zulip"
)

// ZulipChannel implements the Channel interface for Zulip
type ZulipChannel struct {
	bridge     *zulip.Bridge
	msgChan    chan Message
	statusCh   chan StatusUpdate
	reactionCh chan zulip.ReactionReceivedMsg
	stopChan   chan struct{}
}

// ZulipConfig holds configuration for Zulip channel
type ZulipConfig struct {
	SiteURL         string
	Email           string
	APIKey          string
	EventTypes      []string
	UseReactions    bool
	EmergencyCutoff string
	AllowedStreams   []string
}

// NewZulipChannel creates a new Zulip channel
func NewZulipChannel(config ZulipConfig) *ZulipChannel {
	msgChan := make(chan Message, 10)
	statusCh := make(chan StatusUpdate, 5)
	reactionCh := make(chan zulip.ReactionReceivedMsg, 10)
	bridgeMsgChan := make(chan tea.Msg, 10)

	zc := &ZulipChannel{
		msgChan:    msgChan,
		statusCh:   statusCh,
		reactionCh: reactionCh,
		stopChan:   make(chan struct{}),
	}

	// Create Zulip bridge
	zc.bridge = zulip.NewBridge(zulip.BridgeConfig{
		SiteURL:         config.SiteURL,
		Email:           config.Email,
		APIKey:          config.APIKey,
		EventTypes:      config.EventTypes,
		UseReactions:    config.UseReactions,
		EmergencyCutoff: config.EmergencyCutoff,
		AllowedStreams:  config.AllowedStreams,
	}, bridgeMsgChan)

	// Start message adapter goroutine
	go zc.adaptMessages(bridgeMsgChan)

	return zc
}

// Start implements Channel.Start
func (zc *ZulipChannel) Start(ctx context.Context) error {
	if err := zc.bridge.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Zulip bridge: %w", err)
	}
	logging.Info("Zulip channel started")
	return nil
}

// Stop implements Channel.Stop
func (zc *ZulipChannel) Stop(ctx context.Context) error {
	close(zc.stopChan)
	if err := zc.bridge.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop Zulip bridge: %w", err)
	}
	close(zc.msgChan)
	close(zc.statusCh)
	close(zc.reactionCh)
	logging.Info("Zulip channel stopped")
	return nil
}

// IsConnected implements Channel.IsConnected
func (zc *ZulipChannel) IsConnected() bool {
	return zc.bridge.IsConnected()
}

// Messages implements Channel.Messages
func (zc *ZulipChannel) Messages() <-chan Message {
	return zc.msgChan
}

// SendResponse implements Channel.SendResponse
func (zc *ZulipChannel) SendResponse(originalMsg Message, response string, err error) error {
	// Extract original Zulip message from Raw field
	zulipMsg, ok := originalMsg.Raw.(*zulip.Message)
	if !ok {
		return fmt.Errorf("invalid message type for Zulip channel")
	}

	zc.bridge.SendResponse(zulipMsg, response, err)
	return nil
}

// AddReaction implements Channel.AddReaction
func (zc *ZulipChannel) AddReaction(originalMsg Message, emoji string) error {
	// Extract original Zulip message from Raw field
	zulipMsg, ok := originalMsg.Raw.(*zulip.Message)
	if !ok {
		return fmt.Errorf("invalid message type for Zulip channel")
	}

	return zc.bridge.AddReaction(zulipMsg.ID, emoji)
}

// AddReactionByID adds a reaction to a message by its ID
func (zc *ZulipChannel) AddReactionByID(messageID int, emoji string) error {
	return zc.bridge.AddReaction(messageID, emoji)
}

// Name implements Channel.Name
func (zc *ZulipChannel) Name() string {
	return "zulip"
}

// adaptMessages converts Zulip-specific messages to generic Channel messages
func (zc *ZulipChannel) adaptMessages(bridgeMsgChan <-chan tea.Msg) {
	for {
		select {
		case <-zc.stopChan:
			return
		case msg := <-bridgeMsgChan:
			switch m := msg.(type) {
			case zulip.MessageReceivedMsg:
				// Convert Zulip message to generic message
				zulipMsg := m.Message
				genericMsg := Message{
					ID:          strconv.Itoa(zulipMsg.ID),
					Content:     zulipMsg.Content,
					SenderID:    strconv.Itoa(zulipMsg.SenderID),
					SenderName:  zulipMsg.SenderFullName,
					SenderEmail: zulipMsg.SenderEmail,
					Type:       zulipMsg.Type, // "stream" or "private"
					Recipient:  zulipMsg.DisplayRecipient.String(),
					Topic:      zulipMsg.Subject,
					Raw:        zulipMsg, // Store original for SendResponse
				}
				for _, a := range zulipMsg.Attachments {
					genericMsg.Attachments = append(genericMsg.Attachments, Attachment{
						Name:   a.Name,
						PathID: a.PathID,
						Size:   a.Size,
					})
				}

				select {
				case zc.msgChan <- genericMsg:
				case <-zc.stopChan:
					return
				}

			case zulip.ConnectionStatusMsg:
				status := StatusUpdate{
					ChannelName: "zulip",
					Connected:   m.Connected,
					Error:       m.Error,
				}

				select {
				case zc.statusCh <- status:
				case <-zc.stopChan:
					return
				}

			case zulip.ReactionReceivedMsg:
				// Forward reaction events
				select {
				case zc.reactionCh <- m:
				case <-zc.stopChan:
					return
				}
			}
		}
	}
}

// StatusUpdates returns a channel for receiving status updates
func (zc *ZulipChannel) StatusUpdates() <-chan StatusUpdate {
	return zc.statusCh
}

// Reactions returns a channel for receiving reaction events
func (zc *ZulipChannel) Reactions() <-chan zulip.ReactionReceivedMsg {
	return zc.reactionCh
}

// SendMessageWithID sends a message and returns the message ID
func (zc *ZulipChannel) SendMessageWithID(originalMsg Message, content string) (int, error) {
	// Extract original Zulip message from Raw field
	zulipMsg, ok := originalMsg.Raw.(*zulip.Message)
	if !ok {
		return 0, fmt.Errorf("invalid message type for Zulip channel")
	}

	return zc.bridge.SendMessage(zulipMsg, content)
}
