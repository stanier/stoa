package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Default rates, used only when the server's session event omits them.
// Output 24 kHz matches llama.cpp's own example client. The 16 kHz input is
// an assumption (common for speech encoders), not something llama.cpp
// documents; the server's session.created normally carries the real value.
const (
	DefaultInputRate  = 16000
	DefaultOutputRate = 24000
)

// maxUpstreamMessage bounds one message from the model server.
const maxUpstreamMessage = 16 << 20

// LlamaCpp talks to llama-server's WebSocket /v1/realtime endpoint
// (llama.cpp v0.8.0+, PR #31269), which follows the OpenAI Realtime event
// shape. Verified against that PR's example client: session.update with
// type/instructions/output_modalities, input_audio_buffer.append/commit,
// conversation.item.create, response.create/cancel; and session.created,
// response.output_audio.delta, response.output_text.delta, input audio
// transcription delta/completed, response.done and error events.
//
// Not confirmed upstream, so handled defensively: server-side voice activity
// detection (we always end turns explicitly with commit + response.create),
// and the payload field of response.output_audio.delta ("delta" per OpenAI,
// "audio" accepted as a fallback).
type LlamaCpp struct {
	Dialer *websocket.Dialer
}

// Open dials the endpoint and configures the session.
func (b *LlamaCpp) Open(ctx context.Context, opts Options) (Session, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("realtime: no realtime endpoint configured")
	}
	u, err := url.Parse(opts.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, fmt.Errorf("realtime: endpoint must be a ws:// or wss:// URL")
	}
	if opts.Model != "" {
		q := u.Query()
		q.Set("model", opts.Model)
		u.RawQuery = q.Encode()
	}
	hdr := http.Header{}
	if opts.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+opts.APIKey)
	}
	d := b.Dialer
	if d == nil {
		d = &websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	}
	conn, resp, err := d.DialContext(ctx, u.String(), hdr)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("realtime: dial model server: %s", resp.Status)
		}
		return nil, fmt.Errorf("realtime: dial model server: %w", err)
	}
	conn.SetReadLimit(maxUpstreamMessage)
	s := &llamaSession{conn: conn, inRate: DefaultInputRate, outRate: DefaultOutputRate}
	mods := opts.OutputModalities
	if len(mods) == 0 {
		mods = []string{"audio", "text"}
	}
	session := map[string]any{"type": "realtime", "output_modalities": mods}
	if opts.Instructions != "" {
		session["instructions"] = opts.Instructions
	}
	if err := s.write(map[string]any{"type": "session.update", "session": session}); err != nil {
		conn.Close()
		return nil, err
	}
	return s, nil
}

type llamaSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	inRate  int
	outRate int
}

func (s *llamaSession) write(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := s.conn.WriteJSON(v); err != nil {
		return fmt.Errorf("realtime: write to model server: %w", err)
	}
	return nil
}

// Send translates a stoa client event into llama.cpp events.
func (s *llamaSession) Send(ev ClientEvent) error {
	switch ev.Type {
	case ClientAudio:
		return s.write(map[string]any{"type": "input_audio_buffer.append", "audio": ev.Audio})
	case ClientTurnEnd:
		if err := s.write(map[string]any{"type": "input_audio_buffer.commit"}); err != nil {
			return err
		}
		return s.write(map[string]any{"type": "response.create"})
	case ClientText:
		item := map[string]any{
			"type": "message", "role": "user",
			"content": []map[string]any{{"type": "input_text", "text": ev.Text}},
		}
		if err := s.write(map[string]any{"type": "conversation.item.create", "item": item}); err != nil {
			return err
		}
		return s.write(map[string]any{"type": "response.create"})
	case ClientCancel:
		return s.write(map[string]any{"type": "response.cancel"})
	case ClientConfig:
		session := map[string]any{"type": "realtime"}
		if ev.Instructions != "" {
			session["instructions"] = ev.Instructions
		}
		if len(ev.OutputModalities) > 0 {
			session["output_modalities"] = ev.OutputModalities
		}
		return s.write(map[string]any{"type": "session.update", "session": session})
	}
	return ErrBadEvent
}

// upstreamEvent covers the fields we read from llama.cpp events.
type upstreamEvent struct {
	Type       string          `json:"type"`
	Delta      string          `json:"delta"`
	Audio      string          `json:"audio"`
	Transcript string          `json:"transcript"`
	Session    json.RawMessage `json:"session"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Recv returns the next event worth passing to the browser, skipping
// bookkeeping events (item added/done, content parts, ...).
func (s *llamaSession) Recv() (ServerEvent, error) {
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return ServerEvent{}, err
		}
		var up upstreamEvent
		if err := json.Unmarshal(data, &up); err != nil {
			continue
		}
		if ev, ok := s.translate(&up); ok {
			return ev, nil
		}
	}
}

func (s *llamaSession) translate(up *upstreamEvent) (ServerEvent, bool) {
	switch up.Type {
	case "session.created", "session.updated":
		s.readRates(up.Session)
		return ServerEvent{Type: ServerReady, InputRate: s.inRate, OutputRate: s.outRate}, true
	case "response.output_audio.delta":
		audio := up.Delta
		if audio == "" {
			audio = up.Audio
		}
		if audio == "" {
			return ServerEvent{}, false
		}
		return ServerEvent{Type: ServerAudio, Audio: audio}, true
	case "response.output_text.delta", "response.output_audio_transcript.delta":
		if up.Delta == "" {
			return ServerEvent{}, false
		}
		return ServerEvent{Type: ServerTextDelta, Text: up.Delta}, true
	case "conversation.item.input_audio_transcription.delta":
		return ServerEvent{Type: ServerTranscriptDelta, Text: up.Delta}, up.Delta != ""
	case "conversation.item.input_audio_transcription.completed":
		return ServerEvent{Type: ServerTranscriptDone, Text: up.Transcript}, true
	case "response.created":
		return ServerEvent{Type: ServerResponseStarted}, true
	case "response.done":
		return ServerEvent{Type: ServerResponseDone}, true
	case "error":
		msg := "model server error"
		if up.Error != nil && up.Error.Message != "" {
			msg = up.Error.Message
		}
		return ServerEvent{Type: ServerError, Message: msg}, true
	}
	return ServerEvent{}, false
}

// readRates picks audio.input.format.rate / audio.output.format.rate out of
// a session object, keeping the current values when absent.
func (s *llamaSession) readRates(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var sess struct {
		Audio struct {
			Input struct {
				Format struct {
					Rate int `json:"rate"`
				} `json:"format"`
			} `json:"input"`
			Output struct {
				Format struct {
					Rate int `json:"rate"`
				} `json:"format"`
			} `json:"output"`
		} `json:"audio"`
	}
	if json.Unmarshal(raw, &sess) != nil {
		return
	}
	if r := sess.Audio.Input.Format.Rate; r > 0 {
		s.inRate = r
	}
	if r := sess.Audio.Output.Format.Rate; r > 0 {
		s.outRate = r
	}
}

func (s *llamaSession) Close() error {
	s.writeMu.Lock()
	_ = s.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	s.writeMu.Unlock()
	return s.conn.Close()
}
