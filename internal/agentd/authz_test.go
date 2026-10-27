package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stanier/stoa/internal/agentdb"
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

func TestHandleBranchSession_KeepsParentAllowlist(t *testing.T) {
	s, _, shellTool := newAllowlistServer(t)
	parent := restrictedSession(s, []string{"read_file"})
	parent.toolsLocked = true
	s.sessions[parent.id] = parent

	req := httptest.NewRequest(http.MethodPost, "/branch", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.handleBranchSession(rec, req, parent)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("branch: got %d", rec.Code)
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	branch := s.sessions[resp.SessionID]
	if branch == nil {
		t.Fatalf("branch session %q not registered", resp.SessionID)
	}
	if !branch.toolsLocked {
		t.Error("branch lost the parent's tool lock")
	}

	out, _, _ := executeToolCall(context.Background(), s, branch,
		&llm.ToolUse{Name: "shell_exec", Parameters: json.RawMessage(`{}`)}, 0)
	if !strings.HasPrefix(out, "ERROR:") || shellTool.calls.Load() != 0 {
		t.Fatal("branch could run a tool outside the parent's allowlist")
	}
}

// --- #6: session owner comes from the auth context ---

func createSessionAs(t *testing.T, s *Server, r *http.Request) (int, *Session) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleCreateSession(rec, r)
	if rec.Code != http.StatusCreated {
		return rec.Code, nil
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, s.sessions[resp.SessionID]
}

func TestHandleCreateSession_OwnerFromAuthContext(t *testing.T) {
	s := newTestServer(t)

	// Omitting user_id no longer creates an unowned session.
	code, sess := createSessionAs(t, s, asUser(httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{}`)), "alice"))
	if code != http.StatusCreated || sess.userID != "alice" {
		t.Fatalf("got code=%d owner=%q, want 201 alice", code, sess.userID)
	}

	// A user cannot create (and bill) a session as someone else.
	code, _ = createSessionAs(t, s, asUser(httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"user_id":"bob"}`)), "alice"))
	if code != http.StatusForbidden {
		t.Fatalf("spoofed user_id: got %d, want 403", code)
	}

	// A service account (bridge) may create a session on a user's behalf.
	code, sess = createSessionAs(t, s, asService(httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"user_id":"bob"}`)), "zulip-bridge"))
	if code != http.StatusCreated || sess.userID != "bob" {
		t.Fatalf("service: got code=%d owner=%q, want 201 bob", code, sess.userID)
	}
}

func sessionSubrouteAs(s *Server, r *http.Request) int {
	rec := httptest.NewRecorder()
	s.handleSessionSubroute(rec, r)
	return rec.Code
}

func TestHandleSessionSubroute_OwnershipEnforced(t *testing.T) {
	s := newTestServer(t)
	owned := newSession("owned", "", "web", "alice", "", nil)
	unowned := newSession("unowned", "", "web", "", "", nil)
	s.sessions["owned"] = owned
	s.sessions["unowned"] = unowned

	get := func(id string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/messages", nil)
	}
	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"owner", asUser(get("owned"), "alice"), http.StatusOK},
		{"other user", asUser(get("owned"), "bob"), http.StatusForbidden},
		{"unowned, user", asUser(get("unowned"), "bob"), http.StatusForbidden},
		{"unowned, service", asService(get("unowned"), "svc"), http.StatusOK},
		{"no auth configured", get("owned"), http.StatusOK},
	}
	for _, c := range cases {
		if got := sessionSubrouteAs(s, c.req); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHandleListSessions_InMemoryHidesOthersAndUnowned(t *testing.T) {
	s := newTestServer(t)
	s.sessions["a"] = newSession("a", "", "web", "alice", "", nil)
	s.sessions["b"] = newSession("b", "", "web", "bob", "", nil)
	s.sessions["u"] = newSession("u", "", "web", "", "", nil)

	rec := httptest.NewRecorder()
	s.handleListSessions(rec, asUser(httptest.NewRequest(http.MethodGet, "/sessions", nil), "alice"))
	var infos []struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&infos)
	if len(infos) != 1 || infos[0].ID != "a" {
		t.Fatalf("alice sees %+v, want only her own session", infos)
	}
}

func TestHandleDeleteSession_OtherUsersSessionForbidden(t *testing.T) {
	s := newTestServer(t)
	s.sessions["a"] = newSession("a", "", "web", "alice", "", nil)

	rec := httptest.NewRecorder()
	s.handleDeleteSession(rec, asUser(httptest.NewRequest(http.MethodDelete, "/sessions/a", nil), "bob"), "a")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
	if _, still := s.sessions["a"]; !still {
		t.Fatal("session was deleted by a non-owner")
	}
}

// --- #5: agent registry ownership ---

func userCtx(userID string) context.Context {
	return context.WithValue(context.Background(), ctxUserID, userID)
}

func serviceCtx() context.Context {
	ctx := context.WithValue(context.Background(), ctxUserID, "svc")
	return context.WithValue(ctx, ctxIsService, true)
}

func TestPrepareNewAgent_CallerOwnsWhatTheyCreate(t *testing.T) {
	a := agentdb.Agent{Name: "mine", ID: "chosen-id", OwnerUserID: "bob"}
	prepareNewAgent(userCtx("alice"), &a)
	if a.OwnerUserID != "alice" {
		t.Errorf("owner = %q, want alice", a.OwnerUserID)
	}
	if a.ID == "" || a.ID == "chosen-id" {
		t.Errorf("ID = %q, want a server-generated ID", a.ID)
	}

	svc := agentdb.Agent{Name: "provisioned", OwnerUserID: "bob"}
	prepareNewAgent(serviceCtx(), &svc)
	if svc.OwnerUserID != "bob" || svc.ID == "" {
		t.Errorf("service create: owner=%q id=%q", svc.OwnerUserID, svc.ID)
	}
}

func TestPrepareAgentUpdate_OwnerCannotBeReassigned(t *testing.T) {
	existing := &agentdb.Agent{Name: "mine", OwnerUserID: "alice"}
	a := agentdb.Agent{OwnerUserID: "mallory"}
	prepareAgentUpdate(userCtx("alice"), existing, &a)
	if a.OwnerUserID != "alice" {
		t.Errorf("owner reassigned to %q", a.OwnerUserID)
	}
}

func TestCallerMayAccess_SeededAgentsAreServiceOnly(t *testing.T) {
	seeded := &agentdb.Agent{Name: "coder"} // seeded from config: no owner
	if callerMayAccess(userCtx("alice"), seeded.OwnerUserID) {
		t.Error("a user may modify an unowned (seeded) agent")
	}
	if !callerMayAccess(serviceCtx(), seeded.OwnerUserID) {
		t.Error("a service account may not modify a seeded agent")
	}
	if callerMayAccess(userCtx("bob"), "alice") {
		t.Error("a user may modify another user's agent")
	}
	if !callerMayAccess(userCtx("alice"), "alice") {
		t.Error("the owner may not modify their own agent")
	}
}
