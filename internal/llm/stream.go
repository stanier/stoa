package llm

import (
	"context"
	"github.com/stanier/stoa/internal/chat"
)

// StreamHandler is a function that processes stream chunks
type StreamHandler func(chunk StreamChunk) error

// HandleStream processes a streaming response with the given handler
func HandleStream(ctx context.Context, client Client, messages []chat.Message, handler StreamHandler) error {
	stream, err := client.StreamMessage(ctx, messages, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-stream:
			if !ok {
				return nil
			}
			if err := handler(chunk); err != nil {
				return err
			}
			if chunk.Done || chunk.Error != nil {
				return chunk.Error
			}
		}
	}
}
