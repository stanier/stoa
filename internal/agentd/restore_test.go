package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stanier/stoa/internal/agentdb"
	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/llm"
)

// Sessions must come back from an agentd restart with the owner and tool
// limits they were created with. Before this, every restore path rebuilt them
// with no allowlist (so every tool was back) and, from session files, no owner.

// createViaHandler creates a session through POST /sessions and returns it.
func createViaHandler(t *testing.T, s *Server, r *http.Request) *Session {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleCreateSession(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s", rec.Code, rec.Body)
	}
	var created agentproto.CreateSessionResponse
	_ = json.NewDecoder(rec.Body).Decode(&created)
	sess := s.sessions[created.SessionID]
	if sess == nil {
		t.Fatal("created session not registered")
	}
	return sess
}

// assertRestrictedAndLocked checks a restored session that a service account
// created for user u1 with allowed_tools ["read_file"].
func assertRestrictedAndLocked(t *testing.T, s *Server, sess *Session, shellTool *countingTool) {
	t.Helper()
	if sess.userID != "u1" {
		t.Errorf("owner = %q, want u1", sess.userID)
	}
	if len(sess.allowedTools) != 1 || sess.allowedTools[0] != "read_file" {
		t.Errorf("allowedTools = %v, want [read_file]", sess.allowedTools)
	}
	if !sess.toolsLocked {
		t.Error("toolsLocked = false, want true")
	}
	if _, ok := sess.perSessionTools["list_tools"]; !ok {
		t.Error("list_tools not registered on the restored session")
	}

	// The restricted tool still can't run.
	out, _, err := executeToolCall(context.Background(), s, sess,
		&llm.ToolUse{Name: "shell_exec", Parameters: json.RawMessage(`{}`)}, 0)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if !strings.HasPrefix(out, "ERROR:") || shellTool.calls.Load() != 0 {
		t.Fatalf("shell_exec ran in a restored restricted session: %q", out)
	}

	// The owner still can't lift the service account's restriction.
	patch := httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["*"]}`))
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, asUser(patch, "u1"), sess)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("owner PATCH after restore: got %d, want 403", rec.Code)
	}

	// And the owner can still open their own chat.
	if !callerMayAccess(asUser(httptest.NewRequest(http.MethodGet, "/", nil), "u1").Context(), sess.userID) {
		t.Error("owner can't access their restored session")
	}
}

// --- file-backed sessions ---

func TestRestoreFromFile_KeepsOwnerAndToolLimits(t *testing.T) {
	dir := t.TempDir()

	s, _, _ := newAllowlistServer(t)
	s.cfg.Agentd.SessionDir = dir
	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"user_id":"u1","allowed_tools":["read_file"]}`))
	orig := createViaHandler(t, s, asService(create, "svc"))
	if err := saveSession(dir, orig); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	// A fresh process loads it.
	s2, _, shellTool := newAllowlistServer(t)
	s2.cfg.Agentd.SessionDir = dir
	s2.loadPersistedSessions()
	sess := s2.sessions[orig.id]
	if sess == nil {
		t.Fatal("session not restored")
	}
	assertRestrictedAndLocked(t, s2, sess, shellTool)
}

func TestRestoreFromFile_PatchedToolsSurvive(t *testing.T) {
	dir := t.TempDir()

	s, _, _ := newAllowlistServer(t)
	s.cfg.Agentd.SessionDir = dir
	create := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{}`))
	orig := createViaHandler(t, s, asUser(create, "u1"))

	// PATCH writes the session file straight away, before any turn.
	patch := httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["read_file"]}`))
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, asUser(patch, "u1"), orig)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH: got %d", rec.Code)
	}

	s2, _, _ := newAllowlistServer(t)
	s2.cfg.Agentd.SessionDir = dir
	s2.loadPersistedSessions()
	sess := s2.sessions[orig.id]
	if sess == nil {
		t.Fatal("session not restored")
	}
	if sess.userID != "u1" || len(sess.allowedTools) != 1 || sess.allowedTools[0] != "read_file" || sess.toolsLocked {
		t.Fatalf("restored owner=%q tools=%v locked=%v", sess.userID, sess.allowedTools, sess.toolsLocked)
	}
	if !toolAllowedForSession(s2, sess, "read_file") || toolAllowedForSession(s2, sess, "shell_exec") {
		t.Fatal("restored session's tools don't match its allowlist")
	}
}

func TestRestoreFromFile_OldFormatLoadsUnownedAndUnrestricted(t *testing.T) {
	dir := t.TempDir()
	old := `{"id":"legacy-1","persona":"","source":"web","messages":[],"created_at":"2026-01-01T00:00:00Z","last_input_tokens":0,"total_output_tokens":0}`
	if err := os.WriteFile(filepath.Join(dir, "session_legacy-1.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, _ := newAllowlistServer(t)
	s.cfg.Agentd.SessionDir = dir
	s.loadPersistedSessions()
	sess := s.sessions["legacy-1"]
	if sess == nil {
		t.Fatal("legacy session not restored")
	}
	if sess.userID != "" || sess.allowedTools != nil || sess.toolsLocked {
		t.Fatalf("legacy session: owner=%q tools=%v locked=%v", sess.userID, sess.allowedTools, sess.toolsLocked)
	}
}

// --- LLM key ---

func TestReattachLLMKey_OnlyOwner(t *testing.T) {
	s := newTestServer(t)
	sess := newSession("k-1", "", "web", "u1", "", nil)

	connect := func(r *http.Request) *http.Request {
		r.Header.Set(agentproto.LLMKeyHeader, "stoa-infgw_test")
		return r
	}

	// Another user can't attach their key to u1's session.
	s.reattachLLMKey(asUser(connect(httptest.NewRequest(http.MethodGet, "/ws", nil)), "u2"), sess)
	// Nor can a service account.
	s.reattachLLMKey(asService(connect(httptest.NewRequest(http.MethodGet, "/ws", nil)), "svc"), sess)
	if sess.llmKey != "" {
		t.Fatalf("llmKey = %q after non-owner connects, want empty", sess.llmKey)
	}

	// The owner's connect restores it.
	s.reattachLLMKey(asUser(connect(httptest.NewRequest(http.MethodGet, "/ws", nil)), "u1"), sess)
	if sess.llmKey != "stoa-infgw_test" {
		t.Fatalf("llmKey = %q after owner connects", sess.llmKey)
	}

	// A connect without the header leaves it alone.
	s.reattachLLMKey(asUser(httptest.NewRequest(http.MethodGet, "/ws", nil), "u1"), sess)
	if sess.llmKey != "stoa-infgw_test" {
		t.Fatalf("llmKey cleared by a connect without the header")
	}
}

// --- Postgres-backed sessions ---

// newRestoreDBServer returns an allowlist server backed by the database named
// by STOA_TEST_AGENTDB_URL. Skipped when unset.
func newRestoreDBServer(t *testing.T) (*Server, *countingTool) {
	t.Helper()
	dsn := os.Getenv("STOA_TEST_AGENTDB_URL")
	if dsn == "" {
		t.Skip("STOA_TEST_AGENTDB_URL not set; skipping Postgres-backed test")
	}
	ctx := context.Background()
	if err := agentdb.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := agentdb.CheckSchemaVersion(ctx, dsn); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	db, err := agentdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)
	s, _, shellTool := newAllowlistServer(t)
	s.agentDB = db
	return s, shellTool
}

func cleanupSession(t *testing.T, db *agentdb.DB, id string) {
	t.Cleanup(func() { _ = db.DeleteSession(context.Background(), id) })
}

func TestRestoreFromDB_KeepsOwnerAndToolLimits(t *testing.T) {
	s, _ := newRestoreDBServer(t)
	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"user_id":"u1","allowed_tools":["read_file"]}`))
	orig := createViaHandler(t, s, asService(create, "svc"))
	cleanupSession(t, s.agentDB, orig.id)

	// Startup restore of every session.
	s2, shellTool := newRestoreDBServer(t)
	s2.loadSessionsFromDB()
	sess := s2.sessions[orig.id]
	if sess == nil {
		t.Fatal("session not restored at startup")
	}
	assertRestrictedAndLocked(t, s2, sess, shellTool)

	// Lazy restore of one session.
	s3, shellTool3 := newRestoreDBServer(t)
	sess, err := s3.loadSessionFromDB(context.Background(), orig.id)
	if err != nil || sess == nil {
		t.Fatalf("lazy load: %v, %v", sess, err)
	}
	assertRestrictedAndLocked(t, s3, sess, shellTool3)
}

func TestRestoreFromDB_PatchedToolsSurvive(t *testing.T) {
	s, _ := newRestoreDBServer(t)
	orig := createViaHandler(t, s, asUser(httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{}`)), "u1"))
	cleanupSession(t, s.agentDB, orig.id)

	patch := httptest.NewRequest(http.MethodPatch, "/tools", strings.NewReader(`{"allowed_tools":["read_file"]}`))
	rec := httptest.NewRecorder()
	s.handleSessionTools(rec, asUser(patch, "u1"), orig)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH: got %d", rec.Code)
	}

	s2, _ := newRestoreDBServer(t)
	sess, err := s2.loadSessionFromDB(context.Background(), orig.id)
	if err != nil || sess == nil {
		t.Fatalf("lazy load: %v, %v", sess, err)
	}
	if !toolAllowedForSession(s2, sess, "read_file") || toolAllowedForSession(s2, sess, "shell_exec") {
		t.Fatalf("restored tools don't match the PATCHed allowlist %v", sess.allowedTools)
	}
}

func TestRestoreFromDB_BranchKeepsLimits(t *testing.T) {
	s, _ := newRestoreDBServer(t)
	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"user_id":"u1","allowed_tools":["read_file"]}`))
	parent := createViaHandler(t, s, asService(create, "svc"))
	cleanupSession(t, s.agentDB, parent.id)

	rec := httptest.NewRecorder()
	s.handleBranchSession(rec, asUser(httptest.NewRequest(http.MethodPost, "/branch", strings.NewReader(`{}`)), "u1"), parent)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("branch: got %d %s", rec.Code, rec.Body)
	}
	var resp agentproto.BranchSessionResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	cleanupSession(t, s.agentDB, resp.SessionID)

	s2, shellTool := newRestoreDBServer(t)
	sess, err := s2.loadSessionFromDB(context.Background(), resp.SessionID)
	if err != nil || sess == nil {
		t.Fatalf("lazy load branch: %v, %v", sess, err)
	}
	assertRestrictedAndLocked(t, s2, sess, shellTool)
}

// A published agent's tool list applies, and the session's own allowed_tools
// still narrows it, at creation and after a restore.
func TestSessionLLMContext_PublishedAgentNarrowedBySession(t *testing.T) {
	s, _ := newRestoreDBServer(t)
	ctx := context.Background()
	name := "restore-test-" + uuid.NewString()[:8]
	if err := s.agentDB.CreateAgent(ctx, agentdb.Agent{
		Name:         name,
		AllowedTools: []string{"read_file", "shell_exec"},
		Visibility:   agentdb.AgentVisibilityInternal,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = s.agentDB.DeleteAgent(ctx, name) })
	if err := s.agentDB.PublishAgent(ctx, name); err != nil {
		t.Fatalf("publish: %v", err)
	}

	create := httptest.NewRequest(http.MethodPost, "/sessions",
		strings.NewReader(`{"persona":"`+name+`","user_id":"u1","allowed_tools":["read_file"]}`))
	orig := createViaHandler(t, s, asService(create, "svc"))
	cleanupSession(t, s.agentDB, orig.id)
	if toolAllowedForSession(s, orig, "shell_exec") {
		t.Fatal("session allowed_tools ignored for a published agent persona")
	}

	s2, shellTool := newRestoreDBServer(t)
	sess, err := s2.loadSessionFromDB(ctx, orig.id)
	if err != nil || sess == nil {
		t.Fatalf("lazy load: %v, %v", sess, err)
	}
	assertRestrictedAndLocked(t, s2, sess, shellTool)
}
