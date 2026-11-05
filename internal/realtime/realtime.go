// Package realtime carries voice-to-voice conversations between the web UI
// and an "omni" model that takes and produces audio.
//
// The browser speaks a small stoa protocol (ClientEvent / ServerEvent) to
// agentd, and agentd opens a Session on a Backend that translates it to
// whatever the model server speaks. The only Backend today is LlamaCpp, for
// llama.cpp's WebSocket /v1/realtime endpoint (OpenAI Realtime shape, added
// in llama.cpp v0.8.0). Other servers, or a cascaded speech-to-text → LLM →
// text-to-speech pipeline, plug in by implementing Backend.
//
// Audio is mono 16-bit little-endian PCM, base64 in JSON. The sample rates
// come from the model server and reach the browser in the "ready" event; the
// browser resamples to them, because llama.cpp does not resample.
package realtime

import (
	"context"
	"errors"
)

// Client → server event types (browser to agentd).
const (
	// ClientAudio appends a chunk of input audio (Audio: base64 PCM16).
	ClientAudio = "audio"
	// ClientTurnEnd ends the user's spoken turn and asks for a reply.
	ClientTurnEnd = "turn.end"
	// ClientText sends a typed user turn (Text) and asks for a reply.
	ClientText = "text"
	// ClientCancel stops the reply in progress (barge-in).
	ClientCancel = "cancel"
	// ClientConfig changes Instructions / OutputModalities mid-session.
	ClientConfig = "config"
)

// Server → client event types (agentd to browser).
const (
	// ServerReady carries the audio rates. Sent again if the session changes.
	ServerReady = "ready"
	// ServerAudio is a chunk of reply audio (Audio: base64 PCM16).
	ServerAudio = "audio"
	// ServerTextDelta is reply text, or the transcript of reply audio.
	ServerTextDelta = "text.delta"
	// ServerTranscriptDelta / ServerTranscriptDone are the transcript of
	// the user's own speech.
	ServerTranscriptDelta = "transcript.delta"
	ServerTranscriptDone  = "transcript.done"
	// ServerResponseStarted / ServerResponseDone bracket one reply.
	ServerResponseStarted = "response.started"
	ServerResponseDone    = "response.done"
	// ServerError reports a problem; Fatal means the session is over.
	ServerError = "error"
)

// ClientEvent is a message from the browser.
type ClientEvent struct {
	Type             string   `json:"type"`
	Audio            string   `json:"audio,omitempty"`
	Text             string   `json:"text,omitempty"`
	Instructions     string   `json:"instructions,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
}

// ServerEvent is a message to the browser.
type ServerEvent struct {
	Type       string `json:"type"`
	Audio      string `json:"audio,omitempty"`
	Text       string `json:"text,omitempty"`
	InputRate  int    `json:"input_rate,omitempty"`
	OutputRate int    `json:"output_rate,omitempty"`
	Message    string `json:"message,omitempty"`
	Fatal      bool   `json:"fatal,omitempty"`
}

// Options opens a session.
type Options struct {
	// URL is the model server's realtime endpoint (ws:// or wss://).
	URL    string
	APIKey string
	Model  string
	// Instructions is the system prompt for the conversation.
	Instructions string
	// OutputModalities picks the reply form: ["audio", "text"] (default:
	// spoken reply with its transcript), ["audio"] or ["text"].
	OutputModalities []string
}

// Backend opens realtime sessions on a model server.
type Backend interface {
	Open(ctx context.Context, opts Options) (Session, error)
}

// Session is one open conversation. Send and Recv may be called from
// different goroutines; neither may be called concurrently with itself.
type Session interface {
	Send(ev ClientEvent) error
	Recv() (ServerEvent, error)
	Close() error
}

// Limits on what a browser may send.
const (
	// MaxAudioChunk bounds one audio frame (base64). At 24 kHz PCM16 this is
	// over 10 s of audio; the UI sends ~100 ms frames.
	MaxAudioChunk = 768 << 10
	// MaxText bounds a typed turn or instructions.
	MaxText = 32 << 10
)

// ErrBadEvent is returned by Validate for a malformed client event.
var ErrBadEvent = errors.New("realtime: bad client event")

// Validate checks a client event before it is forwarded.
func (ev *ClientEvent) Validate() error {
	switch ev.Type {
	case ClientAudio:
		if ev.Audio == "" || len(ev.Audio) > MaxAudioChunk {
			return ErrBadEvent
		}
	case ClientText:
		if ev.Text == "" || len(ev.Text) > MaxText {
			return ErrBadEvent
		}
	case ClientConfig:
		if len(ev.Instructions) > MaxText {
			return ErrBadEvent
		}
		for _, m := range ev.OutputModalities {
			if m != "audio" && m != "text" {
				return ErrBadEvent
			}
		}
	case ClientTurnEnd, ClientCancel:
	default:
		return ErrBadEvent
	}
	return nil
}
