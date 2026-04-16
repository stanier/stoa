package channels

import (
	"context"
	"fmt"

	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/matrix"
)

// MatrixChannel implements the Channel interface for Matrix.
type MatrixChannel struct {
	baseAdapter
	client *matrix.Client
}

// MatrixConfig holds configuration for the Matrix channel adapter.
type MatrixConfig struct {
	HomeserverURL string
	UserID        string
	AccessToken   string // optional — use instead of password
	Password      string // used if AccessToken is empty
	SyncTokenPath string // optional path for persisting the next_batch token
}

// NewMatrixChannel creates a new MatrixChannel.
func NewMatrixChannel(cfg MatrixConfig) (*MatrixChannel, error) {
	client, err := matrix.NewClient(cfg.HomeserverURL, cfg.UserID, cfg.AccessToken, cfg.Password, cfg.SyncTokenPath)
	if err != nil {
		return nil, fmt.Errorf("matrix channel: %w", err)
	}
	return &MatrixChannel{
		baseAdapter: newBaseAdapter(50),
		client:      client,
	}, nil
}

// Start implements Channel.Start.
func (mc *MatrixChannel) Start(ctx context.Context) error {
	if err := mc.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Matrix client: %w", err)
	}
	go mc.adaptMessages()
	logging.Info("Matrix channel started")
	return nil
}

// Stop implements Channel.Stop.
func (mc *MatrixChannel) Stop(_ context.Context) error {
	close(mc.stopChan)
	mc.client.Stop()
	close(mc.msgChan)
	logging.Info("Matrix channel stopped")
	return nil
}

// SendResponse implements Channel.SendResponse.
func (mc *MatrixChannel) SendResponse(originalMsg Message, response string, _ error) error {
	raw, ok := originalMsg.Raw.(*matrix.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Matrix channel")
	}
	_, err := mc.client.SendMessage(context.Background(), raw.RoomID, response)
	return err
}

// AddReaction implements Channel.AddReaction.
func (mc *MatrixChannel) AddReaction(originalMsg Message, emoji string) error {
	raw, ok := originalMsg.Raw.(*matrix.MessageEvent)
	if !ok {
		return fmt.Errorf("invalid message type for Matrix channel")
	}
	return mc.client.AddReaction(context.Background(), raw.RoomID, raw.EventID, emoji)
}

// Name implements Channel.Name.
func (mc *MatrixChannel) Name() string { return "matrix" }

// StatusUpdates returns the connection status channel.
func (mc *MatrixChannel) StatusUpdates() <-chan matrix.ConnectionStatus {
	return mc.client.StatusUpdates()
}

// Reactions returns the reaction event channel.
func (mc *MatrixChannel) Reactions() <-chan matrix.ReactionEvent {
	return mc.client.Reactions()
}

// SendMessageWithID sends a message and returns the Matrix event ID.
func (mc *MatrixChannel) SendMessageWithID(originalMsg Message, content string) (string, error) {
	raw, ok := originalMsg.Raw.(*matrix.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Matrix channel")
	}
	return mc.client.SendMessage(context.Background(), raw.RoomID, content)
}

// SendFormattedMessage sends a message with HTML formatting. Returns the Matrix event ID.
func (mc *MatrixChannel) SendFormattedMessage(originalMsg Message, body, formattedBody string) (string, error) {
	raw, ok := originalMsg.Raw.(*matrix.MessageEvent)
	if !ok {
		return "", fmt.Errorf("invalid message type for Matrix channel")
	}
	return mc.client.SendFormattedMessage(context.Background(), raw.RoomID, body, formattedBody)
}

// AddReactionByID adds a reaction to an event identified by room ID + event ID.
func (mc *MatrixChannel) AddReactionByID(roomID, eventID, emoji string) error {
	return mc.client.AddReaction(context.Background(), roomID, eventID, emoji)
}

// SendThreadedMessage sends a reply within an m.thread thread. roomID is the
// Matrix room, threadRootEventID is the event ID of the thread root, and body
// is the plain-text message body. Returns the new event's Matrix event ID.
func (mc *MatrixChannel) SendThreadedMessage(roomID, threadRootEventID, body string) (string, error) {
	return mc.client.SendThreadedMessage(context.Background(), roomID, threadRootEventID, body)
}

// adaptMessages converts Matrix MessageEvents to generic Channel messages.
func (mc *MatrixChannel) adaptMessages() {
	for {
		select {
		case <-mc.stopChan:
			return
		case evt, ok := <-mc.client.Messages():
			if !ok {
				return
			}
			raw := evt
			msgType := "direct"
			if !evt.IsDM {
				msgType = "room"
			}
			msg := Message{
				ID:         evt.EventID,
				Content:    evt.Text,
				SenderID:   evt.SenderID,
				SenderName: evt.Username,
				Type:       msgType,
				Recipient:  evt.RoomID,
				// Topic carries the thread root event ID (empty for non-threaded messages).
				Topic: evt.ThreadRootID,
				Raw:   &raw,
			}
			select {
			case mc.msgChan <- msg:
			case <-mc.stopChan:
				return
			}
		}
	}
}
