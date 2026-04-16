package matrix

import (
	"context"
	"fmt"
	"sync"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/stanier/stoa/internal/logging"
)

// Client wraps the mautrix Client with typed event channels.
type Client struct {
	mx *mautrix.Client

	msgCh      chan MessageEvent
	reactionCh chan ReactionEvent
	statusCh   chan ConnectionStatus

	stopChan chan struct{}
	once     sync.Once
}

// NewClient creates a Client. If accessToken is non-empty it is used directly;
// otherwise Login is called with username/password.
// syncTokenPath is an optional file path for persisting the sync token; pass ""
// to use an in-memory store.
func NewClient(homeserverURL, userID, accessToken, password, syncTokenPath string) (*Client, error) {
	mx, err := mautrix.NewClient(homeserverURL, id.UserID(userID), accessToken)
	if err != nil {
		return nil, fmt.Errorf("matrix new client: %w", err)
	}

	if syncTokenPath != "" {
		mx.Store = &FileSyncStore{path: syncTokenPath}
	}

	c := &Client{
		mx:         mx,
		msgCh:      make(chan MessageEvent, 50),
		reactionCh: make(chan ReactionEvent, 50),
		statusCh:   make(chan ConnectionStatus, 10),
		stopChan:   make(chan struct{}),
	}

	if accessToken == "" {
		ctx := context.Background()
		resp, err := mx.Login(ctx, &mautrix.ReqLogin{
			Type: "m.login.password",
			Identifier: mautrix.UserIdentifier{
				Type: "m.id.user",
				User: userID,
			},
			Password:                 password,
			InitialDeviceDisplayName: "stoa-matrix-bridge",
			StoreCredentials:         true,
		})
		if err != nil {
			return nil, fmt.Errorf("matrix login: %w", err)
		}
		logging.Infof("matrix: logged in as %s", resp.UserID)
	}

	syncer := mx.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, c.onMessage)
	syncer.OnEventType(event.EventReaction, c.onReaction)

	return c, nil
}

// Start begins the background sync loop.
func (c *Client) Start(ctx context.Context) error {
	go func() {
		select {
		case c.statusCh <- ConnectionStatus{Connected: true}:
		default:
		}

		err := c.mx.SyncWithContext(ctx)
		if err != nil && ctx.Err() == nil {
			logging.Errorf("matrix: sync loop exited: %v", err)
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
	c.once.Do(func() {
		close(c.stopChan)
		c.mx.StopSync()
	})
}

// Messages returns the channel of incoming MessageEvents.
func (c *Client) Messages() <-chan MessageEvent { return c.msgCh }

// Reactions returns the channel of incoming ReactionEvents.
func (c *Client) Reactions() <-chan ReactionEvent { return c.reactionCh }

// StatusUpdates returns the channel of connection status events.
func (c *Client) StatusUpdates() <-chan ConnectionStatus { return c.statusCh }

// UserID returns the authenticated bot's Matrix user ID.
func (c *Client) UserID() id.UserID { return c.mx.UserID }

// SendMessage sends a text message to a room and returns the event ID.
func (c *Client) SendMessage(ctx context.Context, roomID, text string) (string, error) {
	resp, err := c.mx.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage,
		event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    text,
		})
	if err != nil {
		return "", fmt.Errorf("matrix send message to %s: %w", roomID, err)
	}
	return string(resp.EventID), nil
}

// SendFormattedMessage sends a message with both a plain-text body and an HTML formatted_body.
// Returns the event ID of the sent message.
func (c *Client) SendFormattedMessage(ctx context.Context, roomID, body, formattedBody string) (string, error) {
	resp, err := c.mx.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage,
		event.MessageEventContent{
			MsgType:       event.MsgNotice,
			Body:          body,
			Format:        "org.matrix.custom.html",
			FormattedBody: formattedBody,
		})
	if err != nil {
		return "", fmt.Errorf("matrix send formatted message to %s: %w", roomID, err)
	}
	return string(resp.EventID), nil
}

// SendThreadedMessage sends a text message that is a reply within an m.thread thread.
// threadRootEventID is the event ID of the root event that started the thread.
// Returns the event ID of the sent message.
func (c *Client) SendThreadedMessage(ctx context.Context, roomID, threadRootEventID, body string) (string, error) {
	rel := (&event.RelatesTo{}).SetThread(id.EventID(threadRootEventID), id.EventID(threadRootEventID))
	resp, err := c.mx.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage,
		event.MessageEventContent{
			MsgType:   event.MsgText,
			Body:      body,
			RelatesTo: rel,
		})
	if err != nil {
		return "", fmt.Errorf("matrix send threaded message to %s: %w", roomID, err)
	}
	return string(resp.EventID), nil
}

// AddReaction sends a reaction to an event.
func (c *Client) AddReaction(ctx context.Context, roomID, eventID, emoji string) error {
	_, err := c.mx.SendReaction(ctx, id.RoomID(roomID), id.EventID(eventID), emoji)
	if err != nil {
		return fmt.Errorf("matrix add reaction %q to %s: %w", emoji, eventID, err)
	}
	return nil
}

// IsDMRoom returns true if the room appears to be a direct message (2 members).
func (c *Client) IsDMRoom(ctx context.Context, roomID string) bool {
	resp, err := c.mx.JoinedMembers(ctx, id.RoomID(roomID))
	if err != nil {
		return false
	}
	return len(resp.Joined) == 2
}

// --- sync event handlers ---

func (c *Client) onMessage(ctx context.Context, evt *event.Event) {
	// Skip our own messages.
	if evt.Sender == c.mx.UserID {
		return
	}

	content := evt.Content.AsMessage()
	if content.MsgType != event.MsgText && content.MsgType != event.MsgNotice {
		return
	}

	// Resolve display name from sender localpart as fallback.
	localpart, _, _ := evt.Sender.Parse()
	username := localpart

	// Check for m.thread relation.
	var isThreaded bool
	var threadRootID string
	if rel := content.OptionalGetRelatesTo(); rel != nil && rel.Type == event.RelThread {
		isThreaded = true
		threadRootID = string(rel.EventID)
	}

	msg := MessageEvent{
		RoomID:       string(evt.RoomID),
		EventID:      string(evt.ID),
		SenderID:     string(evt.Sender),
		Username:     username,
		Text:         content.Body,
		IsDM:         true, // let the bridge filter; DM-only MVP
		IsThreaded:   isThreaded,
		ThreadRootID: threadRootID,
	}

	select {
	case c.msgCh <- msg:
	case <-c.stopChan:
	}
}

func (c *Client) onReaction(_ context.Context, evt *event.Event) {
	// Skip our own reactions.
	if evt.Sender == c.mx.UserID {
		return
	}

	content := evt.Content.AsReaction()
	if content.RelatesTo.EventID == "" {
		return
	}

	select {
	case c.reactionCh <- ReactionEvent{
		RoomID:      string(evt.RoomID),
		SenderID:    string(evt.Sender),
		TargetEvent: string(content.RelatesTo.EventID),
		Emoji:       content.RelatesTo.Key,
	}:
	case <-c.stopChan:
	}
}
