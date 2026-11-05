package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/decision"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/realtime"
)

// Seams for tests: how agentd reaches decision and realtime model servers.
var (
	newDecider      = func(url, apiKey string) decision.Decider { return decision.NewClient(url, apiKey) }
	newVoiceBackend = func(pc *config.LLMProviderConfig) realtime.Backend { return &realtime.LlamaCpp{} }
)

// maxDecisionBody bounds a /decisions request (images are inlined base64).
const maxDecisionBody = 16 << 20

// decisionRequest is the body of POST /decisions. Send either questions
// (one call, all answered together) or graph (walked node by node).
type decisionRequest struct {
	Provider  string              `json:"provider"`
	Model     string              `json:"model"`
	State     json.RawMessage     `json:"state"`
	Images    []string            `json:"images,omitempty"`
	Questions []decision.Question `json:"questions,omitempty"`
	Graph     *decision.Graph     `json:"graph,omitempty"`
}

type decisionResponse struct {
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
	Result   *decision.Result `json:"result,omitempty"`
	Trace    *decision.Trace  `json:"trace,omitempty"`
}

// resolveCapableModel finds the provider and model a request names and checks
// the model has the capability. provider "" or "default" means the default
// provider; model "" means that provider's configured model.
func (s *Server) resolveCapableModel(provider, model, capability string) (*config.LLMProviderConfig, string, int, string) {
	if s.providerRegistry == nil {
		return nil, "", http.StatusServiceUnavailable, "no model providers configured"
	}
	if provider == "" {
		provider = "default"
	}
	pc, ok := s.providerRegistry.ProviderConfig(provider)
	if !ok {
		return nil, "", http.StatusNotFound, "unknown provider"
	}
	if model == "" {
		model = pc.Model
	}
	if !pc.HasCapability(model, capability) {
		return nil, "", http.StatusBadRequest, "model " + model + " is not a " + capability + " model (set model_capabilities for it in the provider config)"
	}
	return pc, model, 0, ""
}

// handleDecisions handles POST /decisions: ask a decision model (e.g. d1)
// questions about a state, or walk a decision graph.
func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req decisionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDecisionBody)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if (len(req.Questions) == 0) == (req.Graph == nil) {
		http.Error(w, "send either questions or graph", http.StatusBadRequest)
		return
	}
	pc, model, code, msg := s.resolveCapableModel(req.Provider, req.Model, config.CapabilityDecision)
	if code != 0 {
		http.Error(w, msg, code)
		return
	}
	endpoint := pc.DecisionEndpoint()
	if endpoint == "" {
		http.Error(w, "provider has no decision endpoint (set decision_url)", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	d := newDecider(endpoint, pc.APIKey)
	resp := decisionResponse{Provider: pc.Name, Model: model}

	if req.Graph != nil {
		if err := req.Graph.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tr, err := req.Graph.Walk(ctx, d, model, req.State, req.Images)
		if err != nil {
			writeDecisionError(w, err, tr)
			return
		}
		resp.Trace = tr
	} else {
		dreq := decision.Request{Model: model, State: req.State, Images: req.Images, Questions: req.Questions}
		if err := dreq.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		res, err := d.Decide(ctx, dreq)
		if err != nil {
			writeDecisionError(w, err, nil)
			return
		}
		resp.Result = res
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// writeDecisionError reports a model-side failure as 502 without echoing the
// upstream body (it may carry the upstream's internals).
func writeDecisionError(w http.ResponseWriter, err error, tr *decision.Trace) {
	logging.Warnf("agentd: decisions: %v", err)
	msg := "decision model request failed"
	var he *decision.HTTPError
	if errors.As(err, &he) {
		msg = "decision model returned HTTP " + http.StatusText(he.Status)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "trace": tr})
}

var voiceUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
}

// voiceConn adapts a gorilla WebSocket to realtime.ClientConn. Bridge
// writes from two goroutines (replies and "malformed event" notices), and
// gorilla allows one writer at a time, hence the mutex.
type voiceConn struct {
	c  *websocket.Conn
	mu *sync.Mutex
}

func (v voiceConn) ReadEvent() (realtime.ClientEvent, error) {
	var ev realtime.ClientEvent
	_, data, err := v.c.ReadMessage()
	if err != nil {
		return ev, err
	}
	if json.Unmarshal(data, &ev) != nil {
		ev = realtime.ClientEvent{Type: "invalid"} // Bridge reports and drops it
	}
	return ev, nil
}

func (v voiceConn) WriteEvent(ev realtime.ServerEvent) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	_ = v.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return v.c.WriteJSON(ev)
}

// handleRealtime handles GET /realtime?provider=&model= (WebSocket): a
// voice-to-voice conversation with an omni model. The model server is dialled
// before the upgrade so a bad endpoint fails with a plain HTTP error.
func (s *Server) handleRealtime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	pc, model, code, msg := s.resolveCapableModel(q.Get("provider"), q.Get("model"), config.CapabilityVoice)
	if code != 0 {
		http.Error(w, msg, code)
		return
	}
	endpoint := pc.RealtimeEndpoint()
	if endpoint == "" {
		http.Error(w, "provider has no realtime endpoint (set realtime_url)", http.StatusBadRequest)
		return
	}
	instructions := q.Get("instructions")
	if len(instructions) > realtime.MaxText {
		http.Error(w, "instructions too long", http.StatusBadRequest)
		return
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	sess, err := newVoiceBackend(pc).Open(dialCtx, realtime.Options{
		URL: endpoint, APIKey: pc.APIKey, Model: model, Instructions: instructions,
	})
	cancel()
	if err != nil {
		logging.Warnf("agentd: realtime: open %s/%s: %v", pc.Name, model, err)
		http.Error(w, "could not reach the voice model", http.StatusBadGateway)
		return
	}

	conn, err := voiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = sess.Close()
		logging.Warnf("agentd: realtime: upgrade: %v", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(realtime.MaxAudioChunk + 4<<10)

	userID, _ := UserIDFromContext(r.Context())
	logging.Infof("agentd: realtime: voice session start provider=%s model=%s user=%s", pc.Name, model, userID)
	if err := realtime.Bridge(r.Context(), voiceConn{c: conn, mu: &sync.Mutex{}}, sess); err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		logging.Debugf("agentd: realtime: session ended: %v", err)
	}
	logging.Infof("agentd: realtime: voice session end provider=%s model=%s user=%s", pc.Name, model, userID)
}
