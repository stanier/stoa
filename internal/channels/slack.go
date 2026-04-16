package channels

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/slack"
)

// SlackChannel implements the Channel interface for Slack.
type SlackChannel struct {
	baseAdapter
	client   *slack.Client
	statusCh chan slack.ConnectionStatus
	reactCh  chan slack.ReactionEvent
}

// SlackConfig holds configuration for the Slack channel adapter.
type SlackConfig struct {
	BotToken string
	AppToken string
}

// NewSlackChannel creates a new SlackChannel.
func NewSlackChannel(cfg SlackConfig) *SlackChannel {
	client := slack.NewClient(cfg.BotToken, cfg.AppToken)
	return &SlackChannel{
		baseAdapter: newBaseAdapter(50),
		client:      client,
		statusCh:    make(chan slack.ConnectionStatus, 10),
		reactCh:     make(chan slack.ReactionEvent, 50),
	}
}

// Start implements Channel.Start.
func (sc *SlackChannel) Start(ctx context.Context) error {
	if err := sc.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Slack client: %w", err)
	}
	go sc.adaptMessages()
	logging.Info("Slack channel started")
	return nil
}

// Stop implements Channel.Stop.
func (sc *SlackChannel) Stop(_ context.Context) error {
	close(sc.stopChan)
	sc.client.Stop()
	close(sc.msgChan)
	logging.Info("Slack channel stopped")
	return nil
}

// SendResponse implements Channel.SendResponse.
// Replies in the same channel (or DM) as the original message.
func (sc *SlackChannel) SendResponse(originalMsg Message, response string, _ error) error {
	raw, ok := originalMsg.Raw.(*slack.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Slack channel")
	}
	ctx := context.Background()
	if raw.ThreadTimestamp != "" {
		_, err := sc.client.SendReply(ctx, raw.ChannelID, raw.ThreadTimestamp, response)
		return err
	}
	_, err := sc.client.SendMessage(ctx, raw.ChannelID, response)
	return err
}

// AddReaction implements Channel.AddReaction.
func (sc *SlackChannel) AddReaction(originalMsg Message, emoji string) error {
	raw, ok := originalMsg.Raw.(*slack.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Slack channel")
	}
	return sc.client.AddReaction(context.Background(), raw.ChannelID, raw.Timestamp, emoji)
}

// Name implements Channel.Name.
func (sc *SlackChannel) Name() string {
	return "slack"
}

// StatusUpdates returns the connection status channel.
func (sc *SlackChannel) StatusUpdates() <-chan slack.ConnectionStatus {
	return sc.client.StatusUpdates()
}

// Reactions returns the reaction event channel.
func (sc *SlackChannel) Reactions() <-chan slack.ReactionEvent {
	return sc.client.Reactions()
}

// BlockActions returns the block action event channel (button clicks).
func (sc *SlackChannel) BlockActions() <-chan slack.BlockActionEvent {
	return sc.client.BlockActions()
}

// SendMessageWithID sends a message and returns the Slack timestamp (message ID).
// Used by the bridge to post permission-request messages and track them by timestamp.
func (sc *SlackChannel) SendMessageWithID(originalMsg Message, content string) (string, error) {
	raw, ok := originalMsg.Raw.(*slack.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Slack channel")
	}
	ctx := context.Background()
	if raw.ThreadTimestamp != "" {
		return sc.client.SendReply(ctx, raw.ChannelID, raw.ThreadTimestamp, content)
	}
	return sc.client.SendMessage(ctx, raw.ChannelID, content)
}

// SendBlocksMessageWithID posts a Block Kit message and returns the Slack timestamp.
// If originalMsg is in a thread, the blocks message is posted in that thread.
func (sc *SlackChannel) SendBlocksMessageWithID(originalMsg Message, blocks []map[string]any) (string, error) {
	raw, ok := originalMsg.Raw.(*slack.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Slack channel")
	}
	ctx := context.Background()
	return sc.client.SendBlocksMessage(ctx, raw.ChannelID, raw.ThreadTimestamp, blocks)
}

// AddReactionByTS adds a reaction to a message identified by channel + timestamp.
func (sc *SlackChannel) AddReactionByTS(channelID, messageTS, emoji string) error {
	return sc.client.AddReaction(context.Background(), channelID, messageTS, emoji)
}

// ResolveEmail returns the email address for a Slack user ID.
func (sc *SlackChannel) ResolveEmail(ctx context.Context, userID string) (string, error) {
	return sc.client.GetUserEmail(ctx, userID)
}

// adaptMessages converts Slack MessageEvents to generic Channel messages.
func (sc *SlackChannel) adaptMessages() {
	for {
		select {
		case <-sc.stopChan:
			return

		case evt, ok := <-sc.client.Messages():
			if !ok {
				return
			}
			msgType := "channel"
			if evt.ChannelType == "im" {
				msgType = "direct"
			}
			raw := evt // copy so the pointer is stable
			msg := Message{
				ID:          evt.Timestamp,
				Content:     evt.Text,
				SenderID:    evt.UserID,
				SenderName:  evt.UserID, // display name resolved lazily by bridge if needed
				SenderEmail: "",         // resolved lazily via GetUserEmail
				Type:        msgType,
				Recipient:   evt.ChannelID,
				Topic:       "",
				Raw:         &raw,
			}
			select {
			case sc.msgChan <- msg:
			case <-sc.stopChan:
				return
			}
		}
	}
}
