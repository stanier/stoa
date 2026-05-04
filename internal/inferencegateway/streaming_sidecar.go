package inferencegateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/stanier/stoa/internal/llm"
	"golang.org/x/net/http2"
)

// ─── Wire types ────────────────────────────────────────────────────────────

// SidecarInFrame is one NDJSON line sent from the gateway to the sidecar.
// The sidecar receives these in order and must reply with exactly one
// SidecarOutFrame per frame (maintaining the 1-in/1-out contract).
type SidecarInFrame struct {
	// Type is "chunk" for normal deltas or "done" for the end-of-stream signal.
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`

	// Chunk fields (type == "chunk"):
	Content  string `json:"content,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	// Tool-use fields (type == "chunk", non-empty when a tool call was emitted):
	ToolUseID     string          `json:"tool_use_id,omitempty"`
	ToolUseName   string          `json:"tool_use_name,omitempty"`
	ToolUseParams json.RawMessage `json:"tool_use_params,omitempty"`

	// Done fields (type == "done"):
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// SidecarOutFrame is one NDJSON line sent from the sidecar back to the gateway.
type SidecarOutFrame struct {
	// Type is "ack" (response to a "chunk"), "done" (response to "done"),
	// or "error" (fatal — the gateway will abort the client stream).
	Type string `json:"type"`

	// Transformed content/thinking to forward to the client.
	// Empty string means emit nothing for this chunk (sidecar is buffering).
	Content  string `json:"content,omitempty"`
	Thinking string `json:"thinking,omitempty"`

	// Tool-use fields: pass through unchanged if the sidecar does not modify them.
	ToolUseID     string          `json:"tool_use_id,omitempty"`
	ToolUseName   string          `json:"tool_use_name,omitempty"`
	ToolUseParams json.RawMessage `json:"tool_use_params,omitempty"`

	// Error message (type == "error" only).
	Error string `json:"error,omitempty"`
}

// ─── Client ────────────────────────────────────────────────────────────────

type streamingSidecarTransformer struct {
	requestID string
	model     string
	url       string
	client    *http.Client

	once      sync.Once
	pw        *io.PipeWriter
	enc       *json.Encoder
	outCh     chan SidecarOutFrame
	connErrCh chan error
}

func (t *streamingSidecarTransformer) connect() {
	pr, pw := io.Pipe()
	t.pw = pw
	t.enc = json.NewEncoder(pw)
	t.outCh = make(chan SidecarOutFrame, 8)
	t.connErrCh = make(chan error, 1)

	// Use Background so the long-lived stream is not tied to a single chunk's ctx.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, t.url, pr)
	if err != nil {
		t.connErrCh <- fmt.Errorf("sidecar request: %w", err)
		close(t.outCh)
		return
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	go func() {
		resp, err := t.client.Do(req)
		if err != nil {
			t.connErrCh <- fmt.Errorf("sidecar connect: %w", err)
			close(t.outCh)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.connErrCh <- fmt.Errorf("sidecar status %d", resp.StatusCode)
			close(t.outCh)
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var frame SidecarOutFrame
			if err := json.Unmarshal(line, &frame); err != nil {
				select {
				case t.connErrCh <- fmt.Errorf("sidecar decode: %w", err):
				default:
				}
				break
			}
			t.outCh <- frame
		}
		if err := scanner.Err(); err != nil {
			select {
			case t.connErrCh <- fmt.Errorf("sidecar read: %w", err):
			default:
			}
		}
		close(t.outCh)
	}()
}

// Transform sends chunk to the sidecar and returns the transformed chunk.
// Returns nil chunk (and nil error) when the sidecar emits an empty ack
// (buffering). Returns a non-nil error on sidecar failure; the caller must
// abort the client stream.
func (t *streamingSidecarTransformer) Transform(ctx context.Context, chunk *llm.StreamChunk) (*llm.StreamChunk, error) {
	t.once.Do(t.connect)

	in := chunkToSidecarFrame(t.requestID, t.model, chunk)
	if err := t.enc.Encode(in); err != nil {
		return nil, fmt.Errorf("sidecar write: %w", err)
	}

	select {
	case frame, ok := <-t.outCh:
		if !ok {
			select {
			case err := <-t.connErrCh:
				return nil, err
			default:
				return nil, fmt.Errorf("sidecar closed unexpectedly")
			}
		}
		if frame.Type == "error" {
			return nil, fmt.Errorf("sidecar: %s", frame.Error)
		}
		return sidecarFrameToChunk(frame, chunk), nil
	case err := <-t.connErrCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close signals end-of-stream to the sidecar and releases the connection.
// Safe to call multiple times.
func (t *streamingSidecarTransformer) Close() {
	t.once.Do(t.connect) // ensure fields are initialised even on zero-chunk streams
	if t.pw != nil {
		t.pw.Close()
	}
	// drain so the reader goroutine can exit cleanly
	for range t.outCh {
	}
}

// ─── Frame conversion ──────────────────────────────────────────────────────

func chunkToSidecarFrame(requestID, model string, chunk *llm.StreamChunk) SidecarInFrame {
	f := SidecarInFrame{
		RequestID:    requestID,
		Model:        model,
		Content:      chunk.Content,
		Thinking:     chunk.Thinking,
		InputTokens:  chunk.InputTokens,
		OutputTokens: chunk.OutputTokens,
	}
	if chunk.Done {
		f.Type = "done"
	} else {
		f.Type = "chunk"
	}
	if chunk.ToolUse != nil {
		f.ToolUseID = chunk.ToolUse.ID
		f.ToolUseName = chunk.ToolUse.Name
		f.ToolUseParams = chunk.ToolUse.Parameters
	}
	return f
}

func sidecarFrameToChunk(frame SidecarOutFrame, original *llm.StreamChunk) *llm.StreamChunk {
	out := &llm.StreamChunk{
		Content:      frame.Content,
		Thinking:     frame.Thinking,
		Done:         original.Done,
		InputTokens:  original.InputTokens,
		OutputTokens: original.OutputTokens,
		StopSequence: original.StopSequence,
	}
	if frame.ToolUseID != "" {
		out.ToolUse = &llm.ToolUse{
			ID:         frame.ToolUseID,
			Name:       frame.ToolUseName,
			Parameters: frame.ToolUseParams,
		}
	}
	// If everything is empty, return nil so the caller emits nothing.
	if out.Content == "" && out.Thinking == "" && out.ToolUse == nil && !out.Done {
		return nil
	}
	return out
}

// ─── Factory ───────────────────────────────────────────────────────────────

// WebhookStreamingFactory returns a StreamingTransformFactory backed by the
// sidecar at cfg.StreamingTransformURL. Uses plaintext HTTP/2 (h2c) by
// default; set cfg.StreamingTLS = true for TLS.
func WebhookStreamingFactory(cfg WebhookConfig) StreamingTransformFactory {
	client := newSidecarHTTPClient(cfg.StreamingTLS)
	url := cfg.StreamingTransformURL

	return func(ctx context.Context, requestID, model string) StreamingTransformer {
		return &streamingSidecarTransformer{
			requestID: requestID,
			model:     model,
			url:       url,
			client:    client,
		}
	}
}

func newSidecarHTTPClient(useTLS bool) *http.Client {
	if useTLS {
		tr := &http2.Transport{}
		return &http.Client{Transport: tr}
	}
	// h2c: plaintext HTTP/2 — appropriate for co-located sidecars
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: tr}
}
