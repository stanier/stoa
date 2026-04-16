package channels

import (
	"context"
	"fmt"

	dg "github.com/bwmarrin/discordgo"

	"github.com/stanier/stoa/internal/discord"
	"github.com/stanier/stoa/internal/logging"
)

// DiscordChannel implements the Channel interface for Discord.
type DiscordChannel struct {
	baseAdapter
	client *discord.Client
}

// DiscordConfig holds configuration for the Discord channel adapter.
type DiscordConfig struct {
	BotToken string
}

// NewDiscordChannel creates a new DiscordChannel.
func NewDiscordChannel(cfg DiscordConfig) (*DiscordChannel, error) {
	client, err := discord.NewClient(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("discord channel: %w", err)
	}
	return &DiscordChannel{
		baseAdapter: newBaseAdapter(50),
		client:      client,
	}, nil
}

// Start implements Channel.Start.
func (dc *DiscordChannel) Start(ctx context.Context) error {
	if err := dc.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Discord client: %w", err)
	}
	go dc.adaptMessages()
	logging.Info("Discord channel started")
	return nil
}

// Stop implements Channel.Stop.
func (dc *DiscordChannel) Stop(_ context.Context) error {
	close(dc.stopChan)
	dc.client.Stop()
	close(dc.msgChan)
	logging.Info("Discord channel stopped")
	return nil
}

// SendResponse implements Channel.SendResponse.
func (dc *DiscordChannel) SendResponse(originalMsg Message, response string, _ error) error {
	raw, ok := originalMsg.Raw.(*discord.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Discord channel")
	}
	_, err := dc.client.SendMessage(context.Background(), raw.ChannelID, response)
	return err
}

// AddReaction implements Channel.AddReaction.
func (dc *DiscordChannel) AddReaction(originalMsg Message, emoji string) error {
	raw, ok := originalMsg.Raw.(*discord.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Discord channel")
	}
	return dc.client.AddReaction(context.Background(), raw.ChannelID, raw.MessageID, emoji)
}

// Name implements Channel.Name.
func (dc *DiscordChannel) Name() string { return "discord" }

// StatusUpdates returns the connection status channel.
func (dc *DiscordChannel) StatusUpdates() <-chan discord.ConnectionStatus {
	return dc.client.StatusUpdates()
}

// Reactions returns the reaction event channel.
func (dc *DiscordChannel) Reactions() <-chan discord.ReactionEvent {
	return dc.client.Reactions()
}

// SendMessageWithID sends a message and returns the Discord message ID (snowflake).
func (dc *DiscordChannel) SendMessageWithID(originalMsg Message, content string) (string, error) {
	raw, ok := originalMsg.Raw.(*discord.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Discord channel")
	}
	return dc.client.SendMessage(context.Background(), raw.ChannelID, content)
}

// SendEmbed sends a rich embed and returns the Discord message ID.
func (dc *DiscordChannel) SendEmbed(originalMsg Message, embed *dg.MessageEmbed) (string, error) {
	raw, ok := originalMsg.Raw.(*discord.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Discord channel")
	}
	return dc.client.SendEmbed(context.Background(), raw.ChannelID, embed)
}

// AddReactionByID adds a reaction to a message identified by channel + message ID.
func (dc *DiscordChannel) AddReactionByID(channelID, messageID, emoji string) error {
	return dc.client.AddReaction(context.Background(), channelID, messageID, emoji)
}

// StartThread creates a new public thread in the given guild text channel.
// Returns the new thread's channel ID.
func (dc *DiscordChannel) StartThread(channelID, name string) (string, error) {
	return dc.client.StartThread(channelID, name)
}

// SendMessageToChannelWithID sends a message to an explicit channel ID (e.g. a
// thread) and returns the Discord message ID. This is used when the reply
// target differs from the channel the original message arrived in.
func (dc *DiscordChannel) SendMessageToChannelWithID(channelID, content string) (string, error) {
	return dc.client.SendMessage(context.Background(), channelID, content)
}

// adaptMessages converts Discord MessageEvents to generic Channel messages.
// It resolves whether each message is a DM by checking if we have a DM channel cached;
// for the MVP we forward all messages and let the bridge filter by channel type.
func (dc *DiscordChannel) adaptMessages() {
	for {
		select {
		case <-dc.stopChan:
			return

		case evt, ok := <-dc.client.Messages():
			if !ok {
				return
			}
			raw := evt

			// Determine message type for the bridge.
			msgType := "direct"
			if raw.IsThread {
				msgType = "thread"
			} else if raw.IsGuildChannel {
				msgType = "guild"
			}

			msg := Message{
				ID:         evt.MessageID,
				Content:    evt.Text,
				SenderID:   evt.UserID,
				SenderName: evt.Username,
				Type:       msgType,
				Recipient:  evt.ChannelID,
				// Topic carries the thread ID when the message is in a thread,
				// and the guild ID when it is a plain guild channel message.
				Topic: func() string {
					if raw.IsThread {
						return raw.ThreadID
					}
					return raw.GuildID
				}(),
				Raw: &raw,
			}
			select {
			case dc.msgChan <- msg:
			case <-dc.stopChan:
				return
			}
		}
	}
}
