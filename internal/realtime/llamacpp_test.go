package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeLlama is a minimal stand-in for llama-server's /v1/realtime. It records
// what it receives and answers a turn with audio, text and done events.
type fakeLlama struct {
	mu      sync.Mutex
	got     []map[string]any
	authHdr string
	query   string
	srv     *httptest.Server
	inRate  int
	outRate int
}

func newFakeLlama(t *testing.T) *fakeLlama {
	f := &fakeLlama{inRate: 16000, outRate: 24000}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authHdr = r.Header.Get("Authorization")
		f.query = r.URL.RawQuery
		f.mu.Unlock()
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]any{"type": "session.created", "session": map[string]any{
			"type":  "realtime",
			"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": f.inRate}}, "output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": f.outRate}}},
		}})
		for {
			var ev map[string]any
			if err := c.ReadJSON(&ev); err != nil {
				return
			}
			f.mu.Lock()
			f.got = append(f.got, ev)
			f.mu.Unlock()
			if ev["type"] == "response.create" {
				for _, out := range []map[string]any{
					{"type": "conversation.item.input_audio_transcription.completed", "transcript": "hello there"},
					{"type": "response.created"},
					{"type": "conversation.item.added"}, // bookkeeping, skipped
					{"type": "response.output_audio.delta", "delta": "AAAA"},
					{"type": "response.output_audio.delta", "audio": "BBBB"},
					{"type": "response.output_text.delta", "delta": "Hi!"},
					{"type": "response.done"},
				} {
					_ = c.WriteJSON(out)
				}
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLlama) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/v1/realtime"
}

func (f *fakeLlama) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.got {
		out = append(out, ev["type"].(string))
	}
	return out
}

func recvN(t *testing.T, s Session, n int) []ServerEvent {
	t.Helper()
	var out []ServerEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for len(out) < n {
			ev, err := s.Recv()
			if err != nil {
				return
			}
			out = append(out, ev)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out after %d of %d events", len(out), n)
	}
	return out
}

func TestLlamaCpp_Turn(t *testing.T) {
	f := newFakeLlama(t)
	f.inRate = 16000
	sess, err := (&LlamaCpp{}).Open(context.Background(), Options{
		URL: f.wsURL(), APIKey: "sekret", Model: "LFM3-Omni-2B", Instructions: "Be brief.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	ready := recvN(t, sess, 1)[0]
	if ready.Type != ServerReady || ready.InputRate != 16000 || ready.OutputRate != 24000 {
		t.Fatalf("ready = %+v", ready)
	}

	for _, ev := range []ClientEvent{{Type: ClientAudio, Audio: "UklGRg=="}, {Type: ClientTurnEnd}} {
		if err := sess.Send(ev); err != nil {
			t.Fatal(err)
		}
	}
	evs := recvN(t, sess, 6)
	want := []ServerEvent{
		{Type: ServerTranscriptDone, Text: "hello there"},
		{Type: ServerResponseStarted},
		{Type: ServerAudio, Audio: "AAAA"},
		{Type: ServerAudio, Audio: "BBBB"},
		{Type: ServerTextDelta, Text: "Hi!"},
		{Type: ServerResponseDone},
	}
	for i := range want {
		if evs[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, evs[i], want[i])
		}
	}

	got := f.types()
	wantTypes := []string{"session.update", "input_audio_buffer.append", "input_audio_buffer.commit", "response.create"}
	if strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Errorf("upstream got %v, want %v", got, wantTypes)
	}
	f.mu.Lock()
	first := f.got[0]
	auth, query := f.authHdr, f.query
	f.mu.Unlock()
	sessCfg := first["session"].(map[string]any)
	if sessCfg["instructions"] != "Be brief." || sessCfg["type"] != "realtime" {
		t.Errorf("session.update = %v", first)
	}
	if mods, _ := json.Marshal(sessCfg["output_modalities"]); string(mods) != `["audio","text"]` {
		t.Errorf("output_modalities = %s", mods)
	}
	if auth != "Bearer sekret" || query != "model=LFM3-Omni-2B" {
		t.Errorf("auth=%q query=%q", auth, query)
	}
}

func TestLlamaCpp_TextTurnAndCancel(t *testing.T) {
	f := newFakeLlama(t)
	sess, err := (&LlamaCpp{}).Open(context.Background(), Options{URL: f.wsURL()})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	recvN(t, sess, 1)
	_ = sess.Send(ClientEvent{Type: ClientText, Text: "hi"})
	recvN(t, sess, 6)
	_ = sess.Send(ClientEvent{Type: ClientCancel})
	_ = sess.Send(ClientEvent{Type: ClientConfig, OutputModalities: []string{"text"}})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(f.types()) < 5 {
		time.Sleep(10 * time.Millisecond)
	}
	want := "session.update,conversation.item.create,response.create,response.cancel,session.update"
	if got := strings.Join(f.types(), ","); got != want {
		t.Errorf("upstream got %s, want %s", got, want)
	}
}

func TestLlamaCpp_BadURL(t *testing.T) {
	for _, u := range []string{"", "http://x/v1/realtime", "::"} {
		if _, err := (&LlamaCpp{}).Open(context.Background(), Options{URL: u}); err == nil {
			t.Errorf("Open(%q) should fail", u)
		}
	}
}

func TestReadRatesKeepsDefaults(t *testing.T) {
	s := &llamaSession{inRate: DefaultInputRate, outRate: DefaultOutputRate}
	ev, ok := s.translate(&upstreamEvent{Type: "session.updated", Session: json.RawMessage(`{"type":"realtime"}`)})
	if !ok || ev.InputRate != DefaultInputRate || ev.OutputRate != DefaultOutputRate {
		t.Fatalf("ev = %+v", ev)
	}
	ev, _ = s.translate(&upstreamEvent{Type: "error", Error: &struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	}{Message: "boom"}})
	if ev.Type != ServerError || ev.Message != "boom" {
		t.Fatalf("error ev = %+v", ev)
	}
}

func TestClientEventValidate(t *testing.T) {
	good := []ClientEvent{
		{Type: ClientAudio, Audio: "AA=="}, {Type: ClientTurnEnd}, {Type: ClientCancel},
		{Type: ClientText, Text: "hi"}, {Type: ClientConfig, OutputModalities: []string{"audio"}},
	}
	for _, ev := range good {
		if err := ev.Validate(); err != nil {
			t.Errorf("%+v rejected", ev)
		}
	}
	bad := []ClientEvent{
		{Type: "session.update"}, {Type: ClientAudio}, {Type: ClientText},
		{Type: ClientAudio, Audio: strings.Repeat("A", MaxAudioChunk+1)},
		{Type: ClientConfig, OutputModalities: []string{"video"}},
	}
	for _, ev := range bad {
		if err := ev.Validate(); err == nil {
			t.Errorf("%+v accepted", ev.Type)
		}
	}
}
