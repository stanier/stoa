package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stanier/stoa/internal/agentdb"
)

// newGraphDBServer opens the agentd database named by STOA_TEST_AGENTDB_URL,
// migrates it and empties the decision graphs table. Skipped when unset.
func newGraphDBServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("STOA_TEST_AGENTDB_URL")
	if dsn == "" {
		t.Skip("STOA_TEST_AGENTDB_URL not set; skipping Postgres-backed test")
	}
	ctx := context.Background()
	if err := agentdb.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := agentdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(db.Close)
	if err := agentdb.CheckSchemaVersion(ctx, dsn); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	old, err := db.ListDecisionGraphs(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, g := range old {
		if err := db.DeleteDecisionGraph(ctx, g.ID, ""); err != nil {
			t.Fatalf("clean: %v", err)
		}
	}
	s := newTestServer(t)
	s.agentDB = db
	return s
}

func graphReq(s *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	if req.URL.Path == "/decision-graphs" {
		s.handleDecisionGraphs(rec, req)
	} else {
		s.handleDecisionGraphByID(rec, req)
	}
	return rec
}

func newGraphRequest(method, path, body string) *http.Request {
	return httptest.NewRequest(method, path, strings.NewReader(body))
}

func TestDecisionGraphs_CRUDAndOwnership(t *testing.T) {
	s := newGraphDBServer(t)

	rec := graphReq(s, asUser(newGraphRequest(http.MethodPost, "/decision-graphs",
		`{"name":"  Triage ","graph":{"root":"n1","nodes":[{"id":"n1"}]}}`), "alice"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created decisionGraphWire
	_ = json.NewDecoder(rec.Body).Decode(&created)
	if !strings.HasPrefix(created.ID, "dg_") || created.Name != "Triage" || !strings.Contains(string(created.Graph), `"root"`) {
		t.Fatalf("created = %+v", created)
	}
	path := "/decision-graphs/" + created.ID

	// Bob can't see, change or delete Alice's graph.
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := graphReq(s, asUser(newGraphRequest(m, path, `{"name":"x","graph":{}}`), "bob"))
		if rec.Code != http.StatusNotFound {
			t.Errorf("bob %s: code %d, want 404", m, rec.Code)
		}
	}
	rec = graphReq(s, asUser(newGraphRequest(http.MethodGet, "/decision-graphs", ""), "bob"))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("bob's list = %s", rec.Body)
	}

	// Alice lists (without documents), updates and reads it back.
	rec = graphReq(s, asUser(newGraphRequest(http.MethodGet, "/decision-graphs", ""), "alice"))
	var list []decisionGraphWire
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].ID != created.ID || list[0].Graph != nil {
		t.Fatalf("alice's list = %s", rec.Body)
	}
	rec = graphReq(s, asUser(newGraphRequest(http.MethodPut, path, `{"name":"Renamed","graph":{"root":"n2"}}`), "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	rec = graphReq(s, asUser(newGraphRequest(http.MethodGet, path, ""), "alice"))
	var got decisionGraphWire
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Name != "Renamed" || !strings.Contains(string(got.Graph), `"n2"`) {
		t.Fatalf("got = %+v", got)
	}

	// A service account sees everyone's graphs.
	rec = graphReq(s, asService(newGraphRequest(http.MethodGet, path, ""), "svc"))
	if rec.Code != http.StatusOK {
		t.Errorf("service get: %d", rec.Code)
	}

	rec = graphReq(s, asUser(newGraphRequest(http.MethodDelete, path, ""), "alice"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	rec = graphReq(s, asUser(newGraphRequest(http.MethodGet, path, ""), "alice"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("after delete: %d", rec.Code)
	}
}

func TestDecisionGraphs_Rejects(t *testing.T) {
	s := newGraphDBServer(t)
	cases := map[string]struct {
		body string
		code int
	}{
		"not an object": {`{"name":"a","graph":[1]}`, 400},
		"no graph":      {`{"name":"a"}`, 400},
		"bad json":      {`{`, 400},
		"long name":     {`{"name":"` + strings.Repeat("x", maxDecisionGraphNameLen+1) + `","graph":{}}`, 400},
		"too large":     {`{"name":"a","graph":{"x":"` + strings.Repeat("x", maxDecisionGraphBytes) + `"}}`, 400},
	}
	for name, c := range cases {
		rec := graphReq(s, asUser(newGraphRequest(http.MethodPost, "/decision-graphs", c.body), "alice"))
		if rec.Code != c.code {
			t.Errorf("%s: code %d, want %d (%s)", name, rec.Code, c.code, rec.Body)
		}
	}
	// Writes need someone to own them.
	rec := graphReq(s, newGraphRequest(http.MethodPost, "/decision-graphs", `{"name":"a","graph":{}}`))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous create: code %d", rec.Code)
	}
	rec = graphReq(s, asUser(newGraphRequest(http.MethodPatch, "/decision-graphs/dg_x", ""), "alice"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PATCH: code %d", rec.Code)
	}
}

func TestDecisionGraphs_NoDatabase(t *testing.T) {
	s := newTestServer(t)
	rec := graphReq(s, asUser(newGraphRequest(http.MethodGet, "/decision-graphs", ""), "alice"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code %d, want 503", rec.Code)
	}
}
