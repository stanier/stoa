package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stanier/stoa/internal/app"
	"github.com/stanier/stoa/internal/llm"
	"github.com/stanier/stoa/internal/skills"
)

// countingTool is a skills.Tool that records how many times it ran.
type countingTool struct {
	name  string
	calls atomic.Int32
}

func (t *countingTool) Name() string              { return t.name }
func (t *countingTool) Description() string       { return "test tool " + t.name }
func (t *countingTool) Schema() skills.ToolSchema { return skills.ToolSchema{Type: "object"} }
func (t *countingTool) Execute(context.Context, json.RawMessage) (skills.ToolResult, error) {
	t.calls.Add(1)
	return skills.ToolResult{Success: true, Content: "ran " + t.name}, nil
}

// asUser attaches an authenticated (non-service) user to the request context,
// as AuthMiddleware would.
func asUser(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxUserID, userID)
	return r.WithContext(ctx)
}

// asService attaches a service-account identity to the request context.
func asService(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxUserID, userID)
	ctx = context.WithValue(ctx, ctxIsService, true)
	return r.WithContext(ctx)
}

// --- #1: tool allowlist enforced at execution ---

func newAllowlistServer(t *testing.T) (*Server, *countingTool, *countingTool) {
	t.Helper()
	s := newTestServer(t)
	readTool := &countingTool{name: "read_file"}
	shellTool := &countingTool{name: "shell_exec"}
	s.skillRegistry.RegisterTool(readTool)
	s.skillRegistry.RegisterTool(shellTool)
	return s, readTool, shellTool
}

func restrictedSession(s *Server, allowed []string) *Session {
	sess := newSession("allow-1", "", "web", "u1", "", allowed)
	sess.llmCtx = app.BuildLLMContext(s.cfg, s.skillRegistry, s.promptReg, s.personaReg, "", allowed)
	sess.perSessionTools["list_tools"] = newListToolsTool(&sess.mu, &sess.llmCtx)
	return sess
}

func TestExecuteToolCall_RejectsToolOutsideAllowlist(t *testing.T) {
	s, readTool, shellTool := newAllowlistServer(t)
	sess := restrictedSession(s, []string{"read_file"})

	out, cont, err := executeToolCall(context.Background(), s, sess,
		&llm.ToolUse{Name: "shell_exec", Parameters: json.RawMessage(`{}`)}, 0)
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if !cont || !strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("expected a recoverable tool error, got cont=%v out=%q", cont, out)
	}
	if n := shellTool.calls.Load(); n != 0 {
		t.Fatalf("disallowed tool ran %d time(s)", n)
	}

	if _, _, err := executeToolCall(context.Background(), s, sess,
		&llm.ToolUse{Name: "read_file", Parameters: json.RawMessage(`{}`)}, 0); err != nil {
		t.Fatalf("allowed tool: %v", err)
	}
	if n := readTool.calls.Load(); n != 1 {
		t.Fatalf("allowed tool ran %d time(s), want 1", n)
	}
}

func TestExecuteToolCall_SessionPlumbingToolsAlwaysAllowed(t *testing.T) {
	s, _, _ := newAllowlistServer(t)
	sess := restrictedSession(s, []string{"read_file"})

	out, _, err := executeToolCall(context.Background(), s, sess,
		&llm.ToolUse{Name: "list_tools", Parameters: json.RawMessage(`{}`)}, 0)
	if err != nil {
		t.Fatalf("list_tools: %v", err)
	}
	if strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("list_tools was rejected: %q", out)
	}
}

func TestExecuteToolCall_PerSessionToolShadowingStubIsFiltered(t *testing.T) {
	s, _, _ := newAllowlistServer(t)
	stub := &countingTool{name: "spawn_agent"}
	s.skillRegistry.RegisterTool(stub)
	sess := restrictedSession(s, []string{"read_file"})
	real := &countingTool{name: "spawn_agent"}
	sess.perSessionTools["spawn_agent"] = real

	out, _, _ := executeToolCall(context.Background(), s, sess,
		&llm.ToolUse{Name: "spawn_agent", Parameters: json.RawMessage(`{}`)}, 0)
	if !strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("spawn_agent should be outside the allowlist, got %q", out)
	}
	if real.calls.Load() != 0 || stub.calls.Load() != 0 {
		t.Fatal("spawn_agent ran despite not being allowed")
	}
}

func TestIntersectTools_ChildCannotExceedParent(t *testing.T) {
	parent := []llm.Tool{{Name: "read_file"}}
	child := []llm.Tool{{Name: "read_file"}, {Name: "shell_exec"}}
	got := intersectTools(child, parent)
	if len(got) != 1 || got[0].Name != "read_file" {
		t.Fatalf("intersectTools = %+v, want only read_file", got)
	}
}

func TestHandleSessionTools_PATCH_ServiceRestrictionLockedForOwner(t *testing.T) {
	s, _, _ := newAllowlistServer(t)

	// A bridge (service account) creates a restricted session for user u1.
	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"user_id":"u1","allowed_tools":["read_file"]}`))
	rec := httptest.NewRecorder()
	s.handleCreateSession(rec, asService(create, "svc"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d", rec.Code)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	sess := s.sessions[created.SessionID]

	// The owner may not widen it.
	patch := httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["*"]}`))
	rec = httptest.NewRecorder()
	s.handleSessionTools(rec, asUser(patch, "u1"), sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("owner PATCH: got %d, want 403", rec.Code)
	}

	// The service account still can.
	patch = httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["*"]}`))
	rec = httptest.NewRecorder()
	s.handleSessionTools(rec, asService(patch, "svc"), sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("service PATCH: got %d, want 204", rec.Code)
	}
}

func TestHandleSessionTools_PATCH_UserCreatedSessionStaysEditable(t *testing.T) {
	s, _, _ := newAllowlistServer(t)
	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"allowed_tools":["read_file"]}`))
	rec := httptest.NewRecorder()
	s.handleCreateSession(rec, asUser(create, "u1"))
	var created struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	sess := s.sessions[created.SessionID]

	patch := httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["shell_exec"]}`))
	rec = httptest.NewRecorder()
	s.handleSessionTools(rec, asUser(patch, "u1"), sess)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", rec.Code)
	}
}
