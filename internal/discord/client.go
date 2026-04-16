package discord

import (
	"context"
	"fmt"
	"sync"

	dg "github.com/bwmarrin/discordgo"

	"github.com/stanier/stoa/internal/logging"
)

// Client wraps the discordgo Session with typed event channels.
type Client struct {
	session *dg.Session

	botUserID string

	msgCh      chan MessageEvent
	reactionCh chan ReactionEvent
	statusCh   chan ConnectionStatus

	stopChan chan struct{}
	once     sync.Once
}

// NewClient creates a Client from a bot token (format: "Bot MTk4...").
func NewClient(botToken string) (*Client, error) {
	session, err := dg.New("Bot " + botToken)
	if err != nil {
		return nil, fmt.Errorf("discord new session: %w", err)
	}

	// Request only the intents we need:
	//   IntentDirectMessages          — receive DM MessageCreate events
	//   IntentDirectMessageReactions  — receive DM MessageReactionAdd events
	//   IntentGuildMessages           — receive guild channel MessageCreate events
	//   IntentMessageContent          — privileged: read message content (required for DMs and guild messages)
	session.Identify.Intents = dg.IntentDirectMessages |
		dg.IntentDirectMessageReactions |
		dg.IntentGuildMessages |
		dg.IntentMessageContent

	c := &Client{
		session:    session,
		msgCh:      make(chan MessageEvent, 50),
		reactionCh: make(chan ReactionEvent, 50),
		statusCh:   make(chan ConnectionStatus, 10),
		stopChan:   make(chan struct{}),
	}

	session.AddHandler(c.onReady)
	session.AddHandler(c.onMessageCreate)
	session.AddHandler(c.onMessageReactionAdd)
	session.AddHandler(c.onDisconnect)

	return c, nil
}

// Start opens the Gateway connection.
func (c *Client) Start(_ context.Context) error {
	if err := c.session.Open(); err != nil {
		return fmt.Errorf("discord open session: %w", err)
	}
	return nil
}

// Stop closes the Gateway connection.
func (c *Client) Stop() {
	c.once.Do(func() {
		close(c.stopChan)
		if err := c.session.Close(); err != nil {
			logging.Warnf("discord: close session: %v", err)
		}
	})
}

// Messages returns the channel of incoming MessageEvents.
func (c *Client) Messages() <-chan MessageEvent { return c.msgCh }

// Reactions returns the channel of incoming ReactionEvents.
func (c *Client) Reactions() <-chan ReactionEvent { return c.reactionCh }

// StatusUpdates returns the channel of connection status events.
func (c *Client) StatusUpdates() <-chan ConnectionStatus { return c.statusCh }

// BotUserID returns the authenticated bot's Discord user ID.
func (c *Client) BotUserID() string { return c.botUserID }

// Session returns the underlying discordgo session. Use sparingly.
func (c *Client) Session() *dg.Session { return c.session }

// StartThread creates a new public thread in a guild text channel.
// It returns the new thread's channel ID.
func (c *Client) StartThread(channelID, name string) (string, error) {
	thread, err := c.session.ThreadStart(channelID, name, dg.ChannelTypeGuildPublicThread, 1440)
	if err != nil {
		return "", fmt.Errorf("discord start thread in %s: %w", channelID, err)
	}
	return thread.ID, nil
}

// SendDM opens (or reuses) a DM channel with a user and sends a message,
// returning the sent message ID.
func (c *Client) SendDM(ctx context.Context, userID, text string) (string, error) {
	ch, err := c.session.UserChannelCreate(userID)
	if err != nil {
		return "", fmt.Errorf("discord create DM channel: %w", err)
	}
	msg, err := c.session.ChannelMessageSend(ch.ID, text)
	if err != nil {
		return "", fmt.Errorf("discord send message: %w", err)
	}
	return msg.ID, nil
}

// SendMessage sends a message to any channel and returns the message ID.
func (c *Client) SendMessage(_ context.Context, channelID, text string) (string, error) {
	msg, err := c.session.ChannelMessageSend(channelID, text)
	if err != nil {
		return "", fmt.Errorf("discord send message to %s: %w", channelID, err)
	}
	return msg.ID, nil
}

// SendEmbed sends a rich embed to a channel and returns the message ID.
func (c *Client) SendEmbed(_ context.Context, channelID string, embed *dg.MessageEmbed) (string, error) {
	msg, err := c.session.ChannelMessageSendEmbed(channelID, embed)
	if err != nil {
		return "", fmt.Errorf("discord send embed to %s: %w", channelID, err)
	}
	return msg.ID, nil
}

// AddReaction adds an emoji reaction to a message.
// emoji should be a Unicode emoji string (e.g. "✅") or a name:id pair for custom emoji.
func (c *Client) AddReaction(_ context.Context, channelID, messageID, emoji string) error {
	if err := c.session.MessageReactionAdd(channelID, messageID, emoji); err != nil {
		return fmt.Errorf("discord add reaction %q: %w", emoji, err)
	}
	return nil
}

// --- Gateway event handlers ---

func (c *Client) onReady(_ *dg.Session, r *dg.Ready) {
	c.botUserID = r.User.ID
	logging.Infof("discord: ready as %s#%s (ID: %s)", r.User.Username, r.User.Discriminator, r.User.ID)
	select {
	case c.statusCh <- ConnectionStatus{Connected: true}:
	default:
	}
}

func (c *Client) onDisconnect(_ *dg.Session, _ *dg.Disconnect) {
	logging.Warn("discord: disconnected from Gateway")
	select {
	case c.statusCh <- ConnectionStatus{Connected: false}:
	default:
	}
}

func (c *Client) onMessageCreate(s *dg.Session, m *dg.MessageCreate) {
	// Skip bot messages (including our own).
	if m.Author == nil || m.Author.Bot {
		return
	}
	if m.Type != dg.MessageTypeDefault && m.Type != dg.MessageTypeReply {
		return
	}

	// Determine channel context: DM, guild thread, or guild text channel.
	// We look up the channel to find its type.
	channelType := dg.ChannelTypeDM
	var guildID, threadID string
	isThread := false
	isGuildChannel := false

	if ch, err := s.State.Channel(m.ChannelID); err == nil {
		channelType = ch.Type
		guildID = ch.GuildID
		switch ch.Type {
		case dg.ChannelTypeGuildPublicThread, dg.ChannelTypeGuildPrivateThread, dg.ChannelTypeGuildNewsThread:
			isThread = true
			threadID = m.ChannelID
		case dg.ChannelTypeGuildText, dg.ChannelTypeGuildNews:
			isGuildChannel = true
		}
	} else if m.GuildID != "" {
		// State miss — fall back to GuildID presence.
		guildID = m.GuildID
		// We cannot distinguish thread vs channel without the state; treat as guild channel.
		isGuildChannel = true
		channelType = dg.ChannelTypeGuildText
	}

	select {
	case c.msgCh <- MessageEvent{
		ChannelID:      m.ChannelID,
		UserID:         m.Author.ID,
		Username:       m.Author.Username,
		Text:           m.Content,
		MessageID:      m.ID,
		ChannelType:    int(channelType),
		IsBot:          m.Author.Bot,
		GuildID:        guildID,
		ThreadID:       threadID,
		IsThread:       isThread,
		IsGuildChannel: isGuildChannel,
	}:
	case <-c.stopChan:
	}
}

func (c *Client) onMessageReactionAdd(_ *dg.Session, r *dg.MessageReactionAdd) {
	// Ignore our own reactions.
	if r.UserID == c.botUserID {
		return
	}

	select {
	case c.reactionCh <- ReactionEvent{
		UserID:    r.UserID,
		ChannelID: r.ChannelID,
		MessageID: r.MessageID,
		Emoji:     r.Emoji.Name,
	}:
	case <-c.stopChan:
	}
}
