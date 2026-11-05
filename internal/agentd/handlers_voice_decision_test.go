package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/decision"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/realtime"
)

func newCapabilityTestServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)
	cfg := &config.Config{}
	cfg.LLM.Providers = []config.LLMProviderConfig{
		{Name: "llama", Type: "openai", APIURL: "http://llama:8080/v1", APIKey: "lk",
			Model: "qwen3", Models: []string{"qwen3", "LFM3-Omni-2B"}},
		{Name: "liquid", Type: config.ProviderTypeDecision, APIURL: "https://api.liquid.ai/decisions/v1",
			APIKey: "dk", Model: "d1", Models: []string{"d1"}},
	}
	s.providerRegistry = llm.NewProviderRegistry(cfg)
	return s
}

type recordingDecider struct {
	url, key string
	reqs     []decision.Request
	err      error
}

func (d *recordingDecider) Decide(_ context.Context, req decision.Request) (*decision.Result, error) {
	d.reqs = append(d.reqs, req)
	if d.err != nil {
		return nil, d.err
	}
	res := &decision.Result{Model: req.Model, Usage: &decision.Usage{InputTokens: 5}}
	for _, q := range req.Questions {
		res.Answers = append(res.Answers, decision.Answer{Name: q.Name, Type: q.Type, Pick: "true"})
	}
	return res, nil
}

func stubDecider(t *testing.T, d *recordingDecider) {
	t.Helper()
	orig := newDecider
	newDecider = func(url, key string) decision.Decider { d.url, d.key = url, key; return d }
	t.Cleanup(func() { newDecider = orig })
}

func postDecision(s *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/decisions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleDecisions(rec, req)
	return rec
}

func TestHandleGetModels_Capabilities(t *testing.T) {
	s := newCapabilityTestServer(t)
	rec := httptest.NewRecorder()
	s.handleGetModels(rec, httptest.NewRequest(http.MethodGet, "/models", nil))
	var body struct {
		Providers []struct {
			Name         string              `json:"name"`
			Capabilities map[string][]string `json:"capabilities"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string][]string{}
	for _, p := range body.Providers {
		got[p.Name] = p.Capabilities
	}
	if c := got["llama"]["LFM3-Omni-2B"]; len(c) != 1 || c[0] != "voice" {
		t.Errorf("llama caps = %v", got["llama"])
	}
	if _, ok := got["llama"]["qwen3"]; ok {
		t.Error("plain chat models should have no capabilities entry")
	}
	if c := got["liquid"]["d1"]; len(c) != 1 || c[0] != "decision" {
		t.Errorf("liquid caps = %v", got["liquid"])
	}
}

func TestHandleDecisions_Questions(t *testing.T) {
	s := newCapabilityTestServer(t)
	d := &recordingDecider{}
	stubDecider(t, d)
	rec := postDecision(s, `{"provider":"liquid","state":"app crashes","questions":[{"name":"urgent","type":"noul","instructions":"Urgent?"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	if d.url != "https://api.liquid.ai/decisions/v1/systemone" || d.key != "dk" {
		t.Errorf("decider url=%q key=%q", d.url, d.key)
	}
	if len(d.reqs) != 1 || d.reqs[0].Model != "d1" {
		t.Fatalf("requests = %+v", d.reqs)
	}
	var resp decisionResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Result == nil || resp.Result.Answers[0].Pick != "true" || resp.Provider != "liquid" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestHandleDecisions_Graph(t *testing.T) {
	s := newCapabilityTestServer(t)
	d := &recordingDecider{}
	stubDecider(t, d)
	body := `{"provider":"liquid","model":"d1","state":{"ticket":"refund"},"graph":{"root":"a","nodes":{
		"a":{"question":{"type":"noul","instructions":"A?"},"edges":{"true":"b"}},
		"b":{"question":{"type":"noul","instructions":"B?"}}}}}`
	rec := postDecision(s, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body)
	}
	var resp decisionResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Trace == nil || len(resp.Trace.Steps) != 2 || resp.Trace.Usage.InputTokens != 10 {
		t.Fatalf("trace = %+v", resp.Trace)
	}
}

func TestHandleDecisions_Rejects(t *testing.T) {
	s := newCapabilityTestServer(t)
	stubDecider(t, &recordingDecider{})
	cases := map[string]struct {
		body string
		code int
	}{
		"not a decision model": {`{"provider":"llama","model":"qwen3","state":"x","questions":[{"name":"a","type":"noul","instructions":"?"}]}`, 400},
		"unknown provider":     {`{"provider":"nope","state":"x","questions":[{"name":"a","type":"noul","instructions":"?"}]}`, 404},
		"neither":              {`{"provider":"liquid","state":"x"}`, 400},
		"both":                 {`{"provider":"liquid","state":"x","questions":[{"name":"a","type":"noul","instructions":"?"}],"graph":{"root":"a","nodes":{}}}`, 400},
		"bad question":         {`{"provider":"liquid","state":"x","questions":[{"name":"a","type":"poll","instructions":"?"}]}`, 400},
		"bad graph":            {`{"provider":"liquid","state":"x","graph":{"root":"z","nodes":{"a":{"question":{"type":"noul","instructions":"?"}}}}}`, 400},
		"bad json":             {`{`, 400},
	}
	for name, c := range cases {
		if rec := postDecision(s, c.body); rec.Code != c.code {
			t.Errorf("%s: code %d, want %d (%s)", name, rec.Code, c.code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	s.handleDecisions(rec, httptest.NewRequest(http.MethodGet, "/decisions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code %d", rec.Code)
	}
}

func TestHandleDecisions_UpstreamError(t *testing.T) {
	s := newCapabilityTestServer(t)
	stubDecider(t, &recordingDecider{err: &decision.HTTPError{Status: 500, Body: "secret internals"}})
	rec := postDecision(s, `{"provider":"liquid","state":"x","questions":[{"name":"a","type":"noul","instructions":"?"}]}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
}

// --- /realtime ---

type stubVoiceSession struct {
	mu     sync.Mutex
	sent   []realtime.ClientEvent
	out    chan realtime.ServerEvent
	closed chan struct{}
	once   sync.Once
}

func (v *stubVoiceSession) Send(ev realtime.ClientEvent) error {
	v.mu.Lock()
	v.sent = append(v.sent, ev)
	v.mu.Unlock()
	if ev.Type == realtime.ClientTurnEnd {
		v.out <- realtime.ServerEvent{Type: realtime.ServerAudio, Audio: "AAAA"}
	}
	return nil
}

func (v *stubVoiceSession) Recv() (realtime.ServerEvent, error) {
	select {
	case ev := <-v.out:
		return ev, nil
	case <-v.closed:
		return realtime.ServerEvent{}, io.EOF
	}
}

func (v *stubVoiceSession) Close() error { v.once.Do(func() { close(v.closed) }); return nil }

type stubVoiceBackend struct {
	opts realtime.Options
	sess *stubVoiceSession
	err  error
}

func (b *stubVoiceBackend) Open(_ context.Context, opts realtime.Options) (realtime.Session, error) {
	b.opts = opts
	if b.err != nil {
		return nil, b.err
	}
	b.sess.out <- realtime.ServerEvent{Type: realtime.ServerReady, InputRate: 16000, OutputRate: 24000}
	return b.sess, nil
}

func stubVoice(t *testing.T, b *stubVoiceBackend) {
	t.Helper()
	orig := newVoiceBackend
	newVoiceBackend = func(*config.LLMProviderConfig) realtime.Backend { return b }
	t.Cleanup(func() { newVoiceBackend = orig })
}

func TestHandleRealtime_Bridges(t *testing.T) {
	s := newCapabilityTestServer(t)
	b := &stubVoiceBackend{sess: &stubVoiceSession{out: make(chan realtime.ServerEvent, 4), closed: make(chan struct{})}}
	stubVoice(t, b)
	srv := httptest.NewServer(http.HandlerFunc(s.handleRealtime))
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/realtime?provider=llama&model=LFM3-Omni-2B&instructions=Be+brief"
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))

	var ready realtime.ServerEvent
	if err := c.ReadJSON(&ready); err != nil || ready.Type != realtime.ServerReady || ready.InputRate != 16000 {
		t.Fatalf("ready = %+v err=%v", ready, err)
	}
	_ = c.WriteJSON(realtime.ClientEvent{Type: realtime.ClientAudio, Audio: "AA=="})
	_ = c.WriteJSON(realtime.ClientEvent{Type: realtime.ClientTurnEnd})
	var audio realtime.ServerEvent
	if err := c.ReadJSON(&audio); err != nil || audio.Type != realtime.ServerAudio {
		t.Fatalf("audio = %+v err=%v", audio, err)
	}
	if b.opts.URL != "ws://llama:8080/v1/realtime" || b.opts.APIKey != "lk" || b.opts.Model != "LFM3-Omni-2B" || b.opts.Instructions != "Be brief" {
		t.Errorf("opts = %+v", b.opts)
	}
}

func TestHandleRealtime_Rejects(t *testing.T) {
	s := newCapabilityTestServer(t)
	stubVoice(t, &stubVoiceBackend{err: errors.New("down")})
	for q, code := range map[string]int{
		"provider=llama&model=qwen3":        400,
		"provider=nope&model=LFM3-Omni-2B":  404,
		"provider=llama&model=LFM3-Omni-2B": 502, // backend down: plain HTTP error, no upgrade
	} {
		rec := httptest.NewRecorder()
		s.handleRealtime(rec, httptest.NewRequest(http.MethodGet, "/realtime?"+q, nil))
		if rec.Code != code {
			t.Errorf("%s: code %d, want %d", q, rec.Code, code)
		}
	}
}
