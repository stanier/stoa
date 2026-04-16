package agentd

import (
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/squid"
)

// initSquid writes a generated squid.conf from cfg and waits for the proxy to
// become reachable.  It is a no-op when squid_conf_path is not configured.
func initSquid(cfg *config.Config) {
	if cfg.Agentd.SquidConfPath == "" {
		return
	}

	// Build per-server policies for all docker-transport servers that have a
	// proxy subnet configured.
	var policies []squid.ServerPolicy
	for _, srv := range cfg.MCP.Servers {
		if srv.Transport != "docker" || srv.ProxySubnet == "" {
			continue
		}
		policies = append(policies, squid.ServerPolicy{
			Name:           srv.Name,
			Subnet:         srv.ProxySubnet,
			AllowedDomains: srv.AllowedDomains,
		})
	}

	conf := squid.GenerateConf(policies)

	if err := os.MkdirAll(filepath.Dir(cfg.Agentd.SquidConfPath), 0755); err != nil {
		logging.Warnf("squid: failed to create conf dir: %v", err)
		return
	}
	if err := os.WriteFile(cfg.Agentd.SquidConfPath, []byte(conf), 0644); err != nil {
		logging.Warnf("squid: failed to write conf: %v", err)
		return
	}
	logging.Infof("squid: wrote %s (%d server policies)", cfg.Agentd.SquidConfPath, len(policies))

	// Wait for the proxy to accept connections.
	if cfg.MCP.ProxyAddress == "" {
		return
	}
	waitForProxy(stripScheme(cfg.MCP.ProxyAddress))
}

// waitForProxy polls addr (host:port) until it accepts a TCP connection or the
// deadline is reached.
func waitForProxy(addr string) {
	const (
		pollInterval = 500 * time.Millisecond
		maxWait      = 15 * time.Second
	)
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, pollInterval)
		if err == nil {
			conn.Close()
			logging.Infof("squid: proxy reachable at %s", addr)
			return
		}
		time.Sleep(pollInterval)
	}
	logging.Warnf("squid: proxy at %s not reachable after %s — MCP containers may fail to reach the internet", addr, maxWait)
}

// stripScheme removes a leading "http://" or "https://" prefix.
func stripScheme(addr string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(addr) > len(prefix) && addr[:len(prefix)] == prefix {
			return addr[len(prefix):]
		}
	}
	return addr
}
