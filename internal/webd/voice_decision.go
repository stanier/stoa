package webd

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/stanier/stoa/internal/logging"
)

// Browser → webd voice frames are ~100 ms of PCM16; allow headroom for
// agentd's own per-frame cap (768 KiB base64) plus JSON.
const maxVoiceFrame = 800 << 10

// handleDecisionsProxy proxies POST /api/decisions → agentd /decisions.
func (s *Server) handleDecisionsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	s.proxyToAgentd(w, r, s.cfg.AgentdURL+"/decisions", token)
}

// handleDecisionGraphsProxy proxies /api/decision-graphs[/:id] → agentd
// /decision-graphs[/:id] (saved graphs; agentd checks ownership).
func (s *Server) handleDecisionGraphsProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	tail := strings.TrimPrefix(r.URL.Path, "/api/decision-graphs")
	s.proxyToAgentd(w, r, s.cfg.AgentdURL+"/decision-graphs"+tail, token)
}

// handleRealtimeProxy upgrades GET /api/realtime?provider=&model=[&instructions=]
// to a WebSocket and relays frames to agentd's /realtime, injecting the
// session Bearer token. Only those query parameters are forwarded.
func (s *Server) handleRealtimeProxy(w http.ResponseWriter, r *http.Request) {
	token := s.tokenFromCookie(r)
	if token == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	// Check the origin before dialling, so a refused page never opens a
	// model session upstream. The upgrader checks it again below.
	if s.wsUpgrader.CheckOrigin != nil && !s.wsUpgrader.CheckOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	in := r.URL.Query()
	out := url.Values{}
	for _, k := range []string{"provider", "model", "instructions"} {
		if v := in.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	agentdWS := strings.Replace(s.cfg.AgentdURL, "http://", "ws://", 1)
	agentdWS = strings.Replace(agentdWS, "https://", "wss://", 1)
	agentdWS += "/realtime?" + out.Encode()

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	agentConn, resp, err := dialer.DialContext(r.Context(), agentdWS, hdr)
	if err != nil {
		status := http.StatusBadGateway
		msg := "could not start voice session"
		if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			status, msg = resp.StatusCode, http.StatusText(resp.StatusCode)
		}
		logging.Warnf("webd: realtime proxy: dial agentd: %v", err)
		http.Error(w, msg, status)
		return
	}
	defer agentConn.Close()

	// Origin check: same upgrader (and so same allowed origin) as /ws/.
	browserConn, err := s.wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Errorf("webd: realtime proxy: upgrade browser connection: %v", err)
		return
	}
	defer browserConn.Close()
	browserConn.SetReadLimit(maxVoiceFrame)

	errc := make(chan error, 2)
	relay := func(dst, src *websocket.Conn) {
		for {
			mt, data, err := src.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if mt != websocket.TextMessage {
				continue // the voice protocol is JSON text frames only
			}
			if err := dst.WriteMessage(websocket.TextMessage, data); err != nil {
				errc <- err
				return
			}
		}
	}
	go relay(browserConn, agentConn)
	go relay(agentConn, browserConn)
	if err := <-errc; err != nil {
		logging.Debugf("webd: realtime proxy closed: %v", err)
	}
}
