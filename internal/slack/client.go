package slack

import (
	"context"
	"fmt"
	"sync"
	"time"

	slacklib "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/stanier/stoa/internal/logging"
)

const emailCacheTTL = 30 * time.Minute

// Client wraps the Slack Web API and Socket Mode client.
type Client struct {
	api    *slacklib.Client
	socket *socketmode.Client

	botUserID string

	emailCache   map[string]emailEntry
	emailCacheMu sync.Mutex

	msgCh         chan MessageEvent
	reactionCh    chan ReactionEvent
	blockActionCh chan BlockActionEvent
	statusCh      chan ConnectionStatus

	stopChan chan struct{}
	wg       sync.WaitGroup
}

type emailEntry struct {
	email    string
	cachedAt time.Time
}

// NewClient creates a Client from a bot token and app-level token.
func NewClient(botToken, appToken string) *Client {
	api := slacklib.New(
		botToken,
		slacklib.OptionAppLevelToken(appToken),
	)
	socket := socketmode.New(api)

	return &Client{
		api:           api,
		socket:        socket,
		emailCache:    make(map[string]emailEntry),
		msgCh:         make(chan MessageEvent, 50),
		reactionCh:    make(chan ReactionEvent, 50),
		blockActionCh: make(chan BlockActionEvent, 50),
		statusCh:      make(chan ConnectionStatus, 10),
		stopChan:      make(chan struct{}),
	}
}

// Start connects to Slack Socket Mode and begins the event loop.
func (c *Client) Start(ctx context.Context) error {
	resp, err := c.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("slack auth test: %w", err)
	}
	c.botUserID = resp.UserID
	logging.Infof("slack: authenticated as bot user %s (%s)", resp.UserID, resp.User)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runSocketMode()
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.socket.RunContext(ctx); err != nil && ctx.Err() == nil {
			logging.Errorf("slack: socket mode exited: %v", err)
			select {
			case c.statusCh <- ConnectionStatus{Connected: false, Error: err}:
			default:
			}
		}
	}()

	return nil
}

// Stop shuts down the client.
func (c *Client) Stop() {
	close(c.stopChan)
	c.wg.Wait()
}

// Messages returns the channel of incoming MessageEvents.
func (c *Client) Messages() <-chan MessageEvent { return c.msgCh }

// Reactions returns the channel of incoming ReactionEvents.
func (c *Client) Reactions() <-chan ReactionEvent { return c.reactionCh }

// BlockActions returns the channel of incoming BlockActionEvents (button clicks).
func (c *Client) BlockActions() <-chan BlockActionEvent { return c.blockActionCh }

// StatusUpdates returns the channel of connection status events.
func (c *Client) StatusUpdates() <-chan ConnectionStatus { return c.statusCh }

// BotUserID returns the authenticated bot's Slack user ID.
func (c *Client) BotUserID() string { return c.botUserID }

// SendMessage posts a message to a channel and returns the timestamp (message ID).
func (c *Client) SendMessage(ctx context.Context, channelID, text string) (string, error) {
	_, ts, _, err := c.api.SendMessageContext(ctx, channelID,
		slacklib.MsgOptionText(text, false),
	)
	if err != nil {
		return "", fmt.Errorf("slack send message: %w", err)
	}
	return ts, nil
}

// SendReply posts a threaded reply and returns the reply timestamp.
func (c *Client) SendReply(ctx context.Context, channelID, threadTS, text string) (string, error) {
	_, ts, _, err := c.api.SendMessageContext(ctx, channelID,
		slacklib.MsgOptionText(text, false),
		slacklib.MsgOptionTS(threadTS),
	)
	if err != nil {
		return "", fmt.Errorf("slack send reply: %w", err)
	}
	return ts, nil
}

// SendBlocksMessage posts a Block Kit message to a channel (optionally in a thread)
// and returns the timestamp of the posted message.
func (c *Client) SendBlocksMessage(ctx context.Context, channelID, threadTS string, blocks []map[string]any) (string, error) {
	blockObjects := make([]slacklib.Block, 0, len(blocks))
	for _, b := range blocks {
		blockObjects = append(blockObjects, slacklib.NewRichTextBlock("", slacklib.NewRichTextSection()))
		_ = b // replaced below via raw JSON option
	}

	// Encode blocks as raw JSON and use MsgOptionBlocks via the blocks option.
	// The slack-go SDK's Block types require concrete types; we marshal to JSON
	// and use the UnmarshalJSON path via MsgOptionBlocksAsString.
	// Actually use slack.MsgOptionAttachments workaround: send blocks via text
	// fallback using the proper helper.
	//
	// Use the idiomatic approach: build slacklib.SectionBlock + slacklib.ActionBlock.
	// Since we receive raw map[string]any, we post via chat.postMessage with blocks JSON.
	_ = blockObjects

	opts := []slacklib.MsgOption{
		slacklib.MsgOptionText("", false),
	}
	if threadTS != "" {
		opts = append(opts, slacklib.MsgOptionTS(threadTS))
	}

	// Build Block Kit blocks from the raw map representation.
	rawBlocks := make([]slacklib.Block, 0, len(blocks))
	for _, blk := range blocks {
		switch blk["type"] {
		case "section":
			text, _ := blk["text"].(map[string]any)
			textStr, _ := text["text"].(string)
			rawBlocks = append(rawBlocks, slacklib.NewSectionBlock(
				slacklib.NewTextBlockObject("mrkdwn", textStr, false, false),
				nil, nil,
			))
		case "actions":
			elems, _ := blk["elements"].([]map[string]any)
			btns := make([]slacklib.BlockElement, 0, len(elems))
			for _, el := range elems {
				actionID, _ := el["action_id"].(string)
				value, _ := el["value"].(string)
				labelMap, _ := el["text"].(map[string]any)
				label, _ := labelMap["text"].(string)
				btns = append(btns, slacklib.NewButtonBlockElement(
					actionID,
					value,
					slacklib.NewTextBlockObject("plain_text", label, false, false),
				))
			}
			rawBlocks = append(rawBlocks, slacklib.NewActionBlock("", btns...))
		}
	}
	opts = append(opts, slacklib.MsgOptionBlocks(rawBlocks...))

	_, ts, _, err := c.api.SendMessageContext(ctx, channelID, opts...)
	if err != nil {
		return "", fmt.Errorf("slack send blocks message: %w", err)
	}
	return ts, nil
}

// AddReaction adds an emoji reaction to a message identified by channel + timestamp.
func (c *Client) AddReaction(ctx context.Context, channelID, messageTS, emoji string) error {
	ref := slacklib.ItemRef{Channel: channelID, Timestamp: messageTS}
	if err := c.api.AddReactionContext(ctx, emoji, ref); err != nil {
		return fmt.Errorf("slack add reaction %q: %w", emoji, err)
	}
	return nil
}

// GetUserEmail returns the email address for a Slack user ID.
// Results are cached for emailCacheTTL.
func (c *Client) GetUserEmail(ctx context.Context, userID string) (string, error) {
	c.emailCacheMu.Lock()
	if e, ok := c.emailCache[userID]; ok && time.Since(e.cachedAt) < emailCacheTTL {
		c.emailCacheMu.Unlock()
		return e.email, nil
	}
	c.emailCacheMu.Unlock()

	user, err := c.api.GetUserInfoContext(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("slack get user info for %s: %w", userID, err)
	}
	email := user.Profile.Email

	c.emailCacheMu.Lock()
	c.emailCache[userID] = emailEntry{email: email, cachedAt: time.Now()}
	c.emailCacheMu.Unlock()

	return email, nil
}

// runSocketMode reads Socket Mode events and dispatches them.
func (c *Client) runSocketMode() {
	events := c.socket.Events
	for {
		select {
		case <-c.stopChan:
			return

		case evt, ok := <-events:
			if !ok {
				return
			}

			switch evt.Type {
			case socketmode.EventTypeConnecting:
				logging.Info("slack: connecting to Socket Mode")

			case socketmode.EventTypeConnected:
				logging.Info("slack: connected to Socket Mode")
				select {
				case c.statusCh <- ConnectionStatus{Connected: true}:
				default:
				}

			case socketmode.EventTypeConnectionError:
				logging.Warnf("slack: connection error: %v", evt.Data)
				select {
				case c.statusCh <- ConnectionStatus{Connected: false, Error: fmt.Errorf("connection error")}:
				default:
				}

			case socketmode.EventTypeEventsAPI:
				payload, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					c.socket.Ack(*evt.Request)
					continue
				}
				c.socket.Ack(*evt.Request)
				c.handleEventsAPI(payload)

			case socketmode.EventTypeInteractive:
				callback, ok := evt.Data.(slacklib.InteractionCallback)
				if !ok {
					c.socket.Ack(*evt.Request)
					continue
				}
				c.socket.Ack(*evt.Request)
				if callback.Type == slacklib.InteractionTypeBlockActions {
					c.handleBlockActions(callback)
				}
			}
		}
	}
}

// handleBlockActions dispatches block_actions interaction events.
func (c *Client) handleBlockActions(callback slacklib.InteractionCallback) {
	for _, action := range callback.ActionCallback.BlockActions {
		select {
		case c.blockActionCh <- BlockActionEvent{
			UserID:    callback.User.ID,
			ChannelID: callback.Channel.ID,
			MessageTS: callback.Message.Timestamp,
			ActionID:  action.ActionID,
			Value:     action.Value,
		}:
		case <-c.stopChan:
			return
		}
	}
}

// handleEventsAPI dispatches inner Events API events.
func (c *Client) handleEventsAPI(outer slackevents.EventsAPIEvent) {
	switch outer.InnerEvent.Type {
	case "message":
		data, ok := outer.InnerEvent.Data.(*slackevents.MessageEvent)
		if !ok {
			return
		}
		// Skip bot messages (including our own) and edited/deleted subtypes.
		if data.BotID != "" || data.User == c.botUserID || data.SubType != "" {
			return
		}

		select {
		case c.msgCh <- MessageEvent{
			ChannelID:       data.Channel,
			UserID:          data.User,
			Text:            data.Text,
			Timestamp:       data.TimeStamp,
			ThreadTimestamp: data.ThreadTimeStamp,
			ChannelType:     data.ChannelType,
			BotID:           data.BotID,
		}:
		case <-c.stopChan:
		}

	case "reaction_added":
		data, ok := outer.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
		if !ok {
			return
		}
		if data.Item.Type != "message" || data.User == c.botUserID {
			return
		}

		select {
		case c.reactionCh <- ReactionEvent{
			UserID:    data.User,
			ChannelID: data.Item.Channel,
			MessageTS: data.Item.Timestamp,
			Emoji:     data.Reaction,
		}:
		case <-c.stopChan:
		}
	}
}
