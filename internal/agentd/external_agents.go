package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/stanier/stoa/internal/agentdb"
)

// External agents are either provisioned by an operator (auth-admin or a
// service account: no owner) or registered by a user through POST /agents
// (owned). Operator agents are trusted: they may point at internal services
// and receive the shared AGENTD_PROXY_API_KEY. User agents are not: their URL
// is user input, so agentd must not hand them the shared key or let them make
// agentd fetch internal addresses on their behalf.

// isOperatorAgent reports whether a was provisioned by an operator.
func isOperatorAgent(a agentdb.Agent) bool {
	return a.OwnerUserID == ""
}

// externalAgentAPIKey returns the bearer key agentd sends to an external
// agent: the agent's own metadata "api_key", or, for operator agents only,
// the shared AGENTD_PROXY_API_KEY.
func externalAgentAPIKey(agent agentdb.Agent) string {
	if len(agent.Metadata) > 0 {
		var m map[string]any
		if json.Unmarshal(agent.Metadata, &m) == nil {
			if k, ok := m["api_key"].(string); ok && k != "" {
				return k
			}
		}
	}
	if !isOperatorAgent(agent) {
		return ""
	}
	return os.Getenv("AGENTD_PROXY_API_KEY")
}

// validateExternalURL checks the shape of an agent's external_url.
func validateExternalURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid external_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("external_url must be http or https")
	}
	if u.Host == "" || u.User != nil {
		return fmt.Errorf("external_url must have a host and no credentials")
	}
	return nil
}

// externalAgentHTTPClient returns the client used to proxy a run to agent.
// User-owned agents get a client that can only reach public addresses.
func (s *Server) externalAgentHTTPClient(agent agentdb.Agent) *http.Client {
	if isOperatorAgent(agent) {
		return s.httpClient
	}
	if s.publicHTTPClient == nil {
		return newPublicOnlyHTTPClient(5 * time.Minute)
	}
	return s.publicHTTPClient
}

// newPublicOnlyHTTPClient returns a client whose connections may only go to
// globally routable unicast addresses. The check runs on the address actually
// dialled (after DNS), so DNS rebinding can't get around it, and the client
// ignores HTTP(S)_PROXY so the check isn't applied to the proxy instead.
func newPublicOnlyHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if !isPublicAddr(addr) {
				return fmt.Errorf("external agent address %s is not a public address", addr)
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// nonPublicPrefixes are ranges IsGlobalUnicast and IsPrivate don't exclude
// but that must not be reachable from a user-supplied URL.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"; 0.0.0.0 reaches localhost on Linux
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64 (can map to private IPv4)
}

// isPublicAddr reports whether addr is a globally routable unicast address.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() ||
		addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// redactAgentSecrets removes credentials from an agent's metadata before it is
// shown to someone other than its owner or a service account.
func redactAgentSecrets(ctx context.Context, a *agentdb.Agent) {
	if callerMayAccess(ctx, a.OwnerUserID) || len(a.Metadata) == 0 {
		return
	}
	var m map[string]any
	if json.Unmarshal(a.Metadata, &m) != nil {
		a.Metadata = json.RawMessage("{}")
		return
	}
	if _, ok := m["api_key"]; !ok {
		return
	}
	delete(m, "api_key")
	if b, err := json.Marshal(m); err == nil {
		a.Metadata = b
	} else {
		a.Metadata = json.RawMessage("{}")
	}
}
