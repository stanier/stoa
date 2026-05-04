package inferencegateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookConfig configures the optional transform webhook pair.
// All fields are optional; omit any to skip that phase.
type WebhookConfig struct {
	// PreRequestURL is called after request parsing, before the upstream LLM
	// call. The gateway POSTs a WebhookPreRequest body and expects a
	// WebhookPreResponse back. Any message content returned in the response
	// overwrites the corresponding inbound message.
	PreRequestURL string `yaml:"pre_request_url"`

	// ResponseTransformURL is called after the full response is collected,
	// before it is written to the client. The gateway POSTs a
	// WebhookTransformRequest body and expects a WebhookTransformResponse back.
	// The content field in the response replaces the LLM's content.
	//
	// Not used when StreamingTransformURL is set; the two are mutually
	// exclusive (StreamingTransformURL takes priority for streaming requests).
	ResponseTransformURL string `yaml:"response_transform_url"`

	// StreamingTransformURL enables per-chunk sidecar transforms over a
	// persistent HTTP/2 connection. For each streaming LLM request the gateway
	// opens one POST to this URL, sends NDJSON SidecarInFrames as chunks
	// arrive, and forwards the NDJSON SidecarOutFrames it receives back in
	// real time. See streaming_sidecar.go for the frame schema.
	//
	// When set, ResponseTransformURL is ignored for streaming requests (but
	// still applied to non-streaming requests).
	StreamingTransformURL string `yaml:"streaming_transform_url"`

	// StreamingTLS controls whether the sidecar connection uses TLS.
	// Defaults to false (plaintext HTTP/2 / h2c), which is appropriate for
	// co-located sidecars. Set to true when the sidecar is remote.
	StreamingTLS bool `yaml:"streaming_tls"`

	// Timeout for each non-streaming webhook call (default 10s).
	// Not applied to streaming connections, which are bounded by the LLM
	// response duration.
	Timeout time.Duration `yaml:"timeout"`
}

// WebhookPreRequest is the payload sent to PreRequestURL.
type WebhookPreRequest struct {
	RequestID string           `json:"request_id"`
	Model     string           `json:"model"`
	Messages  []WebhookMessage `json:"messages"`
}

// WebhookMessage is one message in a WebhookPreRequest or WebhookPreResponse.
type WebhookMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// WebhookPreResponse is the response expected from PreRequestURL.
// The gateway replaces each message's content with the corresponding entry
// in Messages (matched by index). Omit Messages or return an empty slice to
// leave messages unchanged.
type WebhookPreResponse struct {
	Messages []WebhookMessage `json:"messages"`
}

// WebhookTransformRequest is the payload sent to ResponseTransformURL.
type WebhookTransformRequest struct {
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	Content   string `json:"content"`
}

// WebhookTransformResponse is the response expected from ResponseTransformURL.
type WebhookTransformResponse struct {
	Content string `json:"content"`
}

// WebhookHooks builds a Hooks bundle wired to the provided WebhookConfig.
// Fields for unconfigured URLs are left nil (no-op).
func WebhookHooks(cfg WebhookConfig) Hooks {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	var h Hooks

	if cfg.StreamingTransformURL != "" {
		h.StreamingTransform = WebhookStreamingFactory(cfg)
	}

	if cfg.PreRequestURL != "" {
		url := cfg.PreRequestURL
		h.PreRequest = append(h.PreRequest, func(ctx context.Context, req *HookRequest) (context.Context, error) {
			payload := WebhookPreRequest{
				RequestID: req.RequestID,
				Model:     req.Model,
				Messages:  make([]WebhookMessage, len(req.Messages)),
			}
			for i, m := range req.Messages {
				payload.Messages[i] = WebhookMessage{Role: string(m.Role), Content: m.Content}
			}

			var resp WebhookPreResponse
			if err := postJSON(ctx, client, url, payload, &resp); err != nil {
				return ctx, fmt.Errorf("pre-request webhook: %w", err)
			}

			for i := range resp.Messages {
				if i >= len(req.Messages) {
					break
				}
				req.Messages[i].Content = resp.Messages[i].Content
			}
			return ctx, nil
		})
	}

	if cfg.ResponseTransformURL != "" {
		url := cfg.ResponseTransformURL
		h.ResponseTransform = append(h.ResponseTransform, func(ctx context.Context, resp *HookResponse) error {
			payload := WebhookTransformRequest{
				RequestID: resp.RequestID,
				Model:     resp.Model,
				Content:   resp.Content,
			}

			var result WebhookTransformResponse
			if err := postJSON(ctx, client, url, payload, &result); err != nil {
				return fmt.Errorf("response transform webhook: %w", err)
			}

			resp.Content = result.Content
			return nil
		})
	}

	return h
}

// postJSON marshals body, POSTs it to url, and decodes the JSON response into out.
func postJSON(ctx context.Context, client *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
