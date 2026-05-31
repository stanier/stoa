package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/llm"
)

// mockLLMForAgentd satisfies llm.Client for agentd handler tests.
type mockLLMForAgentd struct {
	sendResponse string
}

func (m *mockLLMForAgentd) SendMessage(_ context.Context, _ []chat.Message, _ []llm.Tool) (string, error) {
	if m.sendResponse == "" {
		return "summary", nil
	}
	return m.sendResponse, nil
}
func (m *mockLLMForAgentd) StreamMessage(_ context.Context, _ []chat.Message, _ []llm.Tool) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Content: "chunk", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockLLMForAgentd) ListModels(_ context.Context) ([]string, error)        { return nil, nil }
func (m *mockLLMForAgentd) ListModelInfo(_ context.Context) ([]llm.ModelInfo, error) { return nil, nil }

// newTestServerWithLLM returns a test server wired with a mock LLM client.
func newTestServerWithLLM(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)
	s.llmClient = &mockLLMForAgentd{}
	return s
}

// seedSession is a helper that adds a session to the server map and returns it.
func seedSession(s *Server, id string) *Session {
	sess := newSession(id, "default", "tui", "", "", nil)
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess
}

// --- handleClearSession method guard ---

func TestHandleClearSession_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "c1")

	req := httptest.NewRequest(http.MethodGet, "/sessions/c1/clear", nil)
	rec := httptest.NewRecorder()
	s.handleClearSession(rec, req, sess)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleCompactSession ---

func TestHandleCompactSession_CompactionDisabled(t *testing.T) {
	s := newTestServer(t) // cfg.Compaction.Enabled defaults to false
	sess := seedSession(s, "cmp1")

	req := httptest.NewRequest(http.MethodPost, "/sessions/cmp1/compact", nil)
	rec := httptest.NewRecorder()
	s.handleCompactSession(rec, req, sess)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 when compaction disabled", rec.Code)
	}
}

func TestHandleCompactSession_TooFewMessages(t *testing.T) {
	s := newTestServerWithLLM(t)
	s.cfg.Compaction.Enabled = true
	s.cfg.Compaction.KeepRecent = 10

	sess := seedSession(s, "cmp2")
	sess.messages = append(sess.messages, chat.NewUserMessage("hi"))

	req := httptest.NewRequest(http.MethodPost, "/sessions/cmp2/compact", nil)
	rec := httptest.NewRecorder()
	s.handleCompactSession(rec, req, sess)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (too few messages)", rec.Code)
	}
}

func TestHandleCompactSession_WrongMethod(t *testing.T) {
	s := newTestServerWithLLM(t)
	s.cfg.Compaction.Enabled = true
	sess := seedSession(s, "cmp3")

	req := httptest.NewRequest(http.MethodGet, "/sessions/cmp3/compact", nil)
	rec := httptest.NewRecorder()
	s.handleCompactSession(rec, req, sess)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

func TestHandleCompactSession_Success(t *testing.T) {
	s := newTestServerWithLLM(t)
	s.cfg.Compaction = config.CompactionConfig{
		Enabled:          true,
		MaxTokens:        100000,
		CompactThreshold: 80000,
		KeepRecent:       2,
		Strategy:         "sliding_window",
	}

	sess := seedSession(s, "cmp4")
	// Add enough messages to exceed KeepRecent.
	for i := 0; i < 5; i++ {
		sess.messages = append(sess.messages, chat.NewUserMessage("msg"))
		sess.messages = append(sess.messages, chat.NewAssistantMessage("resp"))
	}

	req := httptest.NewRequest(http.MethodPost, "/sessions/cmp4/compact", nil)
	rec := httptest.NewRecorder()
	s.handleCompactSession(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp agentproto.CompactResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.MessagesBefore <= 0 {
		t.Error("MessagesBefore should be positive")
	}
}

// --- handleGetTokens ---

func TestHandleGetTokens_CharEstimatePath(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "tok1")
	sess.messages = append(sess.messages,
		chat.NewUserMessage("hello world"),
		chat.NewAssistantMessage("hi there"),
	)

	req := httptest.NewRequest(http.MethodGet, "/sessions/tok1/tokens", nil)
	rec := httptest.NewRecorder()
	s.handleGetTokens(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp agentproto.TokensResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.TotalMessages != 2 {
		t.Errorf("TotalMessages = %d, want 2", resp.TotalMessages)
	}
}

func TestHandleGetTokens_RealCountPath(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "tok2")
	sess.mu.Lock()
	sess.lastInputTokens = 500
	sess.totalOutputTokens = 100
	sess.mu.Unlock()
	sess.messages = append(sess.messages, chat.NewUserMessage("hi"))

	req := httptest.NewRequest(http.MethodGet, "/sessions/tok2/tokens", nil)
	rec := httptest.NewRecorder()
	s.handleGetTokens(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp agentproto.TokensResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.TotalTokens != 600 {
		t.Errorf("TotalTokens = %d, want 600 (500+100)", resp.TotalTokens)
	}
}

func TestHandleGetTokens_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "tok3")
	req := httptest.NewRequest(http.MethodPost, "/sessions/tok3/tokens", nil)
	rec := httptest.NewRecorder()
	s.handleGetTokens(rec, req, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleGetMessages ---

func TestHandleGetMessages_FiltersSystemMessages(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "msg1")
	sess.messages = append(sess.messages,
		systemMessage("system prompt"),
		chat.NewUserMessage("user msg"),
		chat.NewAssistantMessage("assistant msg"),
	)

	req := httptest.NewRequest(http.MethodGet, "/sessions/msg1/messages", nil)
	rec := httptest.NewRecorder()
	s.handleGetMessages(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var msgs []chat.Message
	_ = json.NewDecoder(rec.Body).Decode(&msgs)
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2 (system filtered)", len(msgs))
	}
	for _, m := range msgs {
		if m.Role == chat.RoleSystem {
			t.Error("system message should have been filtered out")
		}
	}
}

func TestHandleGetMessages_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "msg2")
	req := httptest.NewRequest(http.MethodPost, "/sessions/msg2/messages", nil)
	rec := httptest.NewRecorder()
	s.handleGetMessages(rec, req, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleRestoreMessages ---

func TestHandleRestoreMessages_Success(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "rst1")
	sess.messages = append(sess.messages,
		systemMessage("sys"),
		chat.NewUserMessage("old msg"),
	)

	newMsgs := []chat.Message{
		chat.NewUserMessage("restored 1"),
		chat.NewAssistantMessage("restored 2"),
	}
	body, _ := json.Marshal(newMsgs)

	req := httptest.NewRequest(http.MethodPost, "/sessions/rst1/restore", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleRestoreMessages(rec, req, sess)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}

	sess.mu.Lock()
	total := len(sess.messages)
	sess.mu.Unlock()
	// system + 2 restored = 3
	if total != 3 {
		t.Errorf("messages after restore = %d, want 3", total)
	}
}

func TestHandleRestoreMessages_InvalidJSON(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "rst2")

	req := httptest.NewRequest(http.MethodPost, "/sessions/rst2/restore", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	s.handleRestoreMessages(rec, req, sess)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestHandleRestoreMessages_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "rst3")
	req := httptest.NewRequest(http.MethodGet, "/sessions/rst3/restore", nil)
	rec := httptest.NewRecorder()
	s.handleRestoreMessages(rec, req, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleGetUsage ---

func TestHandleGetUsage_NoDBNoUser(t *testing.T) {
	s := newTestServer(t) // agentDB is nil
	req := httptest.NewRequest(http.MethodGet, "/sessions/usage", nil)
	rec := httptest.NewRecorder()
	s.handleGetUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["message"] == "" {
		t.Error("expected graceful 'not available' message")
	}
}

func TestHandleGetUsage_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions/usage", nil)
	rec := httptest.NewRecorder()
	s.handleGetUsage(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleGetModels ---

func TestHandleGetModels_NoRegistry(t *testing.T) {
	s := newTestServer(t) // providerRegistry is nil
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	s.handleGetModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	providers, ok := resp["providers"].([]interface{})
	if !ok {
		t.Fatal("response missing 'providers' array")
	}
	if len(providers) != 0 {
		t.Errorf("expected empty providers list, got %d", len(providers))
	}
}

func TestHandleGetModels_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/models", nil)
	rec := httptest.NewRecorder()
	s.handleGetModels(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleSessionConfig ---

func TestHandleSessionConfig_GET_NilOverrides(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "cfg1")

	req := httptest.NewRequest(http.MethodGet, "/sessions/cfg1/config", nil)
	rec := httptest.NewRecorder()
	s.handleSessionConfig(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var o llm.Overrides
	if err := json.NewDecoder(rec.Body).Decode(&o); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
}

func TestHandleSessionConfig_PATCH_MergesFields(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "cfg2")

	temp := 0.7
	maxTok := 1024
	patch := llm.Overrides{
		Temperature: &temp,
		MaxTokens:   &maxTok,
	}
	body, _ := json.Marshal(patch)

	req := httptest.NewRequest(http.MethodPatch, "/sessions/cfg2/config", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSessionConfig(rec, req, sess)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}

	sess.mu.Lock()
	o := sess.overrides
	sess.mu.Unlock()
	if o == nil {
		t.Fatal("overrides should not be nil after PATCH")
	}
	if o.Temperature == nil || *o.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", o.Temperature)
	}
	if o.MaxTokens == nil || *o.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %v, want 1024", o.MaxTokens)
	}
}

func TestHandleSessionConfig_PATCH_InvalidJSON(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "cfg3")

	req := httptest.NewRequest(http.MethodPatch, "/sessions/cfg3/config", strings.NewReader("bad json"))
	rec := httptest.NewRecorder()
	s.handleSessionConfig(rec, req, sess)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestHandleSessionConfig_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "cfg4")

	req := httptest.NewRequest(http.MethodDelete, "/sessions/cfg4/config", nil)
	rec := httptest.NewRecorder()
	s.handleSessionConfig(rec, req, sess)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleListTools ---

func TestHandleListTools_EmptyRegistry(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tools", nil)
	rec := httptest.NewRecorder()
	s.handleListTools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var tools []interface{}
	_ = json.NewDecoder(rec.Body).Decode(&tools)
	if len(tools) != 0 {
		t.Errorf("expected empty tool list, got %d", len(tools))
	}
}

func TestHandleListTools_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/tools", nil)
	rec := httptest.NewRecorder()
	s.handleListTools(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleSessionTools ---

func TestHandleSessionTools_GET_Empty(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "st1")

	req := httptest.NewRequest(http.MethodGet, "/sessions/st1/tools", nil)
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, req, sess)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	tools, ok := resp["allowed_tools"].([]interface{})
	if !ok {
		t.Fatal("response missing 'allowed_tools'")
	}
	if len(tools) != 0 {
		t.Errorf("expected empty allowed_tools, got %d", len(tools))
	}
}

func TestHandleSessionTools_PATCH_SetsTools(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "st2")

	body := `{"allowed_tools": ["bash", "read"]}`
	req := httptest.NewRequest(http.MethodPatch, "/sessions/st2/tools", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, req, sess)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}

	sess.mu.Lock()
	allowed := sess.allowedTools
	sess.mu.Unlock()
	if len(allowed) != 2 {
		t.Errorf("allowed_tools len = %d, want 2", len(allowed))
	}
}

func TestHandleSessionTools_PATCH_InvalidJSON(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "st3")
	req := httptest.NewRequest(http.MethodPatch, "/sessions/st3/tools", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, req, sess)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
}

func TestHandleSessionTools_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	sess := seedSession(s, "st4")
	req := httptest.NewRequest(http.MethodDelete, "/sessions/st4/tools", nil)
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, req, sess)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleACPPing ---

func TestHandleACPPing(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/acp/ping", nil)
	rec := httptest.NewRecorder()
	s.handleACPPing(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != "{}" {
		t.Errorf("body = %q, want {}", body)
	}
}

// --- handleACPListAgents ---

func TestHandleACPListAgents_NoDB_ReturnsEmptyList(t *testing.T) {
	s := newTestServer(t) // agentDB is nil
	req := httptest.NewRequest(http.MethodGet, "/acp/agents", nil)
	rec := httptest.NewRecorder()
	s.handleACPListAgents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	agents, ok := resp["agents"].([]interface{})
	if !ok {
		t.Fatal("response missing 'agents' array")
	}
	if len(agents) != 0 {
		t.Errorf("expected empty agents list, got %d", len(agents))
	}
}

func TestHandleACPListAgents_WrongMethod(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/acp/agents", nil)
	rec := httptest.NewRecorder()
	s.handleACPListAgents(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d, want 405", rec.Code)
	}
}

// --- handleACPAgentByName ---

func TestHandleACPAgentByName_NoDB_Returns503(t *testing.T) {
	s := newTestServer(t) // agentDB is nil
	req := httptest.NewRequest(http.MethodGet, "/acp/agents/myagent", nil)
	rec := httptest.NewRecorder()
	s.handleACPAgentByName(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 when no agent registry", rec.Code)
	}
}

func TestHandleACPAgentByName_EmptyName_Returns404(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/acp/agents/", nil)
	rec := httptest.NewRecorder()
	s.handleACPAgentByName(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 for empty agent name", rec.Code)
	}
}

// --- handleListSessions session info ---

func TestHandleListSessions_IncludesPersonaAndSource(t *testing.T) {
	s := newTestServer(t)

	sess := newSession("info1", "coder", "web", "user-x", "", nil)
	s.mu.Lock()
	s.sessions["info1"] = sess
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	rec := httptest.NewRecorder()
	s.handleListSessions(rec, req)

	var sessions []agentproto.SessionInfo
	_ = json.NewDecoder(rec.Body).Decode(&sessions)

	found := false
	for _, si := range sessions {
		if si.ID == "info1" {
			found = true
			if si.Persona != "coder" {
				t.Errorf("Persona = %q, want coder", si.Persona)
			}
			if si.Source != "web" {
				t.Errorf("Source = %q, want web", si.Source)
			}
		}
	}
	if !found {
		t.Error("session info1 not found in list")
	}
}
