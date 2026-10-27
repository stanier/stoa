package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stanier/stoa/internal/agentdb"
)

// --- #4: external agents can't take the shared key or reach internal services ---

func TestExternalAgentAPIKey_SharedKeyOnlyForOperatorAgents(t *testing.T) {
	t.Setenv("AGENTD_PROXY_API_KEY", "shared-secret")

	operator := agentdb.Agent{Name: "ops"}
	if got := externalAgentAPIKey(operator); got != "shared-secret" {
		t.Errorf("operator agent: key = %q, want the shared key", got)
	}

	userAgent := agentdb.Agent{Name: "mine", OwnerUserID: "mallory"}
	if got := externalAgentAPIKey(userAgent); got != "" {
		t.Errorf("user agent: key = %q, want none", got)
	}

	userAgent.Metadata = json.RawMessage(`{"api_key":"own-key"}`)
	if got := externalAgentAPIKey(userAgent); got != "own-key" {
		t.Errorf("user agent with its own key: key = %q, want own-key", got)
	}
}

func TestIsPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":          true,
		"2606:4700::1111":  true,
		"127.0.0.1":        false,
		"10.1.2.3":         false,
		"172.16.0.1":       false,
		"192.168.1.1":      false,
		"169.254.169.254":  false, // cloud metadata
		"0.0.0.0":          false,
		"0.1.2.3":          false,
		"100.64.0.1":       false,
		"::1":              false,
		"::":               false,
		"fc00::1":          false,
		"fe80::1":          false,
		"::ffff:127.0.0.1": false,
		"64:ff9b::a00:1":   false,
	}
	for s, want := range cases {
		if got := isPublicAddr(netip.MustParseAddr(s)); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestProxyACPRun_UserAgentCannotReachInternalAddress(t *testing.T) {
	t.Setenv("AGENTD_PROXY_API_KEY", "shared-secret")
	var hits atomic.Int32
	var gotAuth atomic.Value
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	s := newTestServer(t)
	s.publicHTTPClient = newPublicOnlyHTTPClient(0)

	userAgent := agentdb.Agent{Name: "mine", OwnerUserID: "mallory", ExternalURL: internal.URL}
	rec := httptest.NewRecorder()
	s.proxyACPRun(rec, httptest.NewRequest(http.MethodPost, "/acp/runs", nil), userAgent, acpRunRequest{AgentName: "mine"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("user agent at a loopback URL: got %d, want 502", rec.Code)
	}
	if hits.Load() != 0 {
		t.Fatal("agentd made a request to an internal address for a user agent")
	}

	// Operator-provisioned agents keep working against internal services and
	// still get the shared key.
	operator := agentdb.Agent{Name: "ops", ExternalURL: internal.URL}
	rec = httptest.NewRecorder()
	s.proxyACPRun(rec, httptest.NewRequest(http.MethodPost, "/acp/runs", nil), operator, acpRunRequest{AgentName: "ops"})
	if rec.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("operator agent: code=%d hits=%d", rec.Code, hits.Load())
	}
	if gotAuth.Load() != "Bearer shared-secret" {
		t.Fatalf("operator agent auth header = %v", gotAuth.Load())
	}
}

func TestRedactAgentSecrets_HidesOtherUsersKeys(t *testing.T) {
	a := agentdb.Agent{Name: "ext", OwnerUserID: "alice", Metadata: json.RawMessage(`{"api_key":"k","team":"x"}`)}
	redactAgentSecrets(userCtx("bob"), &a)
	var m map[string]any
	_ = json.Unmarshal(a.Metadata, &m)
	if _, ok := m["api_key"]; ok {
		t.Fatal("api_key shown to a non-owner")
	}
	if m["team"] != "x" {
		t.Fatal("non-secret metadata was dropped")
	}

	own := agentdb.Agent{Name: "ext", OwnerUserID: "alice", Metadata: json.RawMessage(`{"api_key":"k"}`)}
	redactAgentSecrets(userCtx("alice"), &own)
	if string(own.Metadata) != `{"api_key":"k"}` {
		t.Fatal("owner's own key was redacted")
	}
}

func TestValidateExternalURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"":                         true,
		"https://agents.example/x": true,
		"http://agents.example":    true,
		"file:///etc/passwd":       false,
		"gopher://x":               false,
		"https://user:pw@host/":    false,
		"https:///nohost":          false,
	} {
		if err := validateExternalURL(raw); (err == nil) != ok {
			t.Errorf("validateExternalURL(%q) err=%v, want ok=%v", raw, err, ok)
		}
	}
}
