package knowledgebase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedder produces embedding vectors for text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// noopEmbedder returns nil embeddings (used when no embed URL is configured).
type noopEmbedder struct{}

func (n *noopEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

// HTTPEmbedder calls an OpenAI-compatible /v1/embeddings endpoint.
// Works with OpenAI, Ollama (nomic-embed-text, mxbai-embed-large, etc.),
// and any other compatible provider.
type HTTPEmbedder struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

// NewHTTPEmbedder creates an embedder pointing at the given base URL.
// model defaults to "nomic-embed-text" if empty.
func NewHTTPEmbedder(baseURL, model, apiKey string) *HTTPEmbedder {
	if model == "" {
		model = "nomic-embed-text"
	}
	return &HTTPEmbedder{
		url:    strings.TrimRight(baseURL, "/"),
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed sends texts to the embedding API in batches of 64 and returns vectors.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	const batchSize = 64
	result := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := e.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		copy(result[i:], batch)
	}
	return result, nil
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (e *HTTPEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embeddingRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.url+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedding API returned %d: %s", resp.StatusCode, raw)
	}

	var result embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index >= 0 && d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
