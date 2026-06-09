package agentd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/skills"
)

// newTestServer builds a minimal agentd Server suitable for handler unit tests.
// It wires real metrics (isolated Prometheus registry), empty registries, and
// an in-memory session map with no DB or MCP.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{}
	promReg := prometheus.NewRegistry()
	metrics := NewMetrics(promReg, nil)

	return &Server{
		cfg:           cfg,
		skillRegistry: skills.NewRegistry(),
		promptReg:     app.SetupPromptRegistry(cfg),
		personaReg:    app.SetupPersonaRegistry(),
		sessions:      make(map[string]*Session),
		acpRuns:       make(map[string]*ACPRun),
		metrics:       metrics,
		promReg:       promReg,
		metricsHandler: http.NotFoundHandler(),
		httpClient:    &http.Client{},
	}
}

// --- POST /sessions ---

func TestHandleCreateSession_NoBody(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusCreated)
	}

	var resp agentproto.CreateSessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.SessionID == "" {
		t.Error("expected non-empty session_id")
	}

	s.mu.RLock()
	_, ok := s.sessions[resp.SessionID]
	s.mu.RUnlock()
	if !ok {
		t.Errorf("session %s not found in server.sessions", resp.SessionID)
	}
}

func TestHandleCreateSession_WithPersona(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(agentproto.SessionConfig{Persona: "coder", Source: "web"})
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateSession(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusCreated)
	}

	var resp agentproto.CreateSessionResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	s.mu.RLock()
	sess := s.sessions[resp.SessionID]
	s.mu.RUnlock()

	if sess == nil {
		t.Fatal("session not found")
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.persona != "coder" {
		t.Errorf("persona = %q, want coder", sess.persona)
	}
	if sess.source != "web" {
		t.Errorf("source = %q, want web", sess.source)
	}
}

func TestHandleCreateSession_InvalidJSON(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader("{bad json}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateSession(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- GET /sessions ---

func TestHandleListSessions_Empty(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	rec := httptest.NewRecorder()

	s.handleListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	var sessions []agentproto.SessionInfo
	if err := json.NewDecoder(rec.Body).Decode(&sessions); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected empty list, got %d sessions", len(sessions))
	}
}

func TestHandleListSessions_ReturnsSessions(t *testing.T) {
	s := newTestServer(t)

	// Create two sessions directly.
	s.mu.Lock()
	s.sessions["sess-1"] = newSession("sess-1", "default", "tui", "user-a", "", nil)
	s.sessions["sess-2"] = newSession("sess-2", "coder", "web", "user-b", "", nil)
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	rec := httptest.NewRecorder()
	s.handleListSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	var sessions []agentproto.SessionInfo
	_ = json.NewDecoder(rec.Body).Decode(&sessions)
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

// --- DELETE via subroute ---

func TestHandleDeleteSession_NotFound(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/sessions/nonexistent", nil)
	rec := httptest.NewRecorder()

	s.handleDeleteSession(rec, req, "nonexistent")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestHandleDeleteSession_Existing(t *testing.T) {
	s := newTestServer(t)

	// Pre-create a session.
	sess := newSession("del-1", "default", "tui", "", "", nil)
	s.mu.Lock()
	s.sessions["del-1"] = sess
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodDelete, "/sessions/del-1", nil)
	rec := httptest.NewRecorder()
	s.handleDeleteSession(rec, req, "del-1")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}

	s.mu.RLock()
	_, exists := s.sessions["del-1"]
	s.mu.RUnlock()
	if exists {
		t.Error("session should have been removed from the map")
	}
}

// --- POST /sessions/:id/clear ---

func TestHandleClearSession_ResetsMessages(t *testing.T) {
	s := newTestServer(t)
	sess := newSession("clr-1", "default", "tui", "", "", nil)
	sess.messages = append(sess.messages, systemMessage("You are helpful."))
	sess.messages = append(sess.messages, chat.NewUserMessage("Hello"))
	s.mu.Lock()
	s.sessions["clr-1"] = sess
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/sessions/clr-1/clear", nil)
	rec := httptest.NewRecorder()
	s.handleClearSession(rec, req, sess)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}

	sess.mu.Lock()
	count := len(sess.messages)
	sess.mu.Unlock()
	// Only the leading system message should remain.
	if count != 1 {
		t.Errorf("messages after clear = %d, want 1 (system only)", count)
	}
}

// --- GET /sessions/:id/tokens ---

func TestHandleGetTokens_ReturnsJSON(t *testing.T) {
	s := newTestServer(t)
	sess := newSession("tok-1", "default", "tui", "", "", nil)
	s.mu.Lock()
	s.sessions["tok-1"] = sess
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/sessions/tok-1/tokens", nil)
	rec := httptest.NewRecorder()
	s.handleGetTokens(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Error("expected Content-Type: application/json")
	}
}

// --- Router smoke test ---

func TestRouter_Routes(t *testing.T) {
	s := newTestServer(t)
	router := s.Router()
	if router == nil {
		t.Fatal("Router() returned nil")
	}

	// Verify a few known endpoints respond (not 404/405 from routing black hole).
	paths := []struct {
		method      string
		path        string
		body        string
		contentType string
		want        int
	}{
		{http.MethodGet, "/sessions", "", "", http.StatusOK},
		{http.MethodPost, "/sessions", "{}", "application/json", http.StatusCreated},
		{http.MethodGet, "/models", "", "", http.StatusOK},
		{http.MethodGet, "/acp/ping", "", "", http.StatusOK},
	}

	for _, tc := range paths {
		var bodyReader *strings.Reader
		if tc.body != "" {
			bodyReader = strings.NewReader(tc.body)
		} else {
			bodyReader = strings.NewReader("")
		}
		req := httptest.NewRequest(tc.method, tc.path, bodyReader)
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
