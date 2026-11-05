package llm

import (
	"context"
	"errors"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
)

// ErrDecisionOnly is returned when a chat call reaches a provider that only
// serves decision models. Decisions go through agentd's /decisions endpoint.
var ErrDecisionOnly = errors.New("llm: this provider only serves decision models; use /decisions instead of chat")

// decisionOnlyClient stands in for a chat client on a "decision" provider so
// the provider and its models still show up in the registry.
type decisionOnlyClient struct {
	models []string
}

func newDecisionOnlyClient(pc *config.LLMProviderConfig) *decisionOnlyClient {
	models := append([]string(nil), pc.Models...)
	if len(models) == 0 && pc.Model != "" {
		models = []string{pc.Model}
	}
	return &decisionOnlyClient{models: models}
}

func (c *decisionOnlyClient) SendMessage(context.Context, []chat.Message, []Tool) (string, error) {
	return "", ErrDecisionOnly
}

func (c *decisionOnlyClient) StreamMessage(context.Context, []chat.Message, []Tool) (<-chan StreamChunk, error) {
	return nil, ErrDecisionOnly
}

// ListModels returns the configured list: the System One API has no model
// listing endpoint.
func (c *decisionOnlyClient) ListModels(context.Context) ([]string, error) {
	return append([]string(nil), c.models...), nil
}

func (c *decisionOnlyClient) ListModelInfo(ctx context.Context) ([]ModelInfo, error) {
	ids, _ := c.ListModels(ctx)
	out := make([]ModelInfo, len(ids))
	for i, id := range ids {
		out[i] = ModelInfo{ID: id}
	}
	return out, nil
}
