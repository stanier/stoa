package webd

import (
	"os"
	"strings"
)

// Config holds webd runtime configuration, resolved from environment variables.
type Config struct {
	// Port is the HTTP listen port. Default: 3000.
	Port string

	// AuthdURL is the internal URL of the authd service.
	// e.g. "http://authd:8888"
	AuthdURL string

	// AgentdURL is the internal URL of the agentd service.
	// e.g. "http://agentd:7777"
	AgentdURL string

	// ExternalURL is the public URL webd is reachable at from browsers.
	// Used to construct the post_login_redirect sent to authd.
	// e.g. "http://localhost:3000" or "https://app.example.com"
	ExternalURL string

	// APIKeydURL is the internal URL of the apikeyd service.
	// e.g. "http://apikeyd:8889". When empty, /api/apikeys returns 503.
	APIKeydURL string
}

// LoadConfig reads webd configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:         envOr("WEBD_PORT", "3000"),
		AuthdURL:     strings.TrimRight(envOr("WEBD_AUTHD_URL", "http://authd:8888"), "/"),
		AgentdURL:    strings.TrimRight(envOr("WEBD_AGENTD_URL", "http://agentd:7777"), "/"),
		APIKeydURL:  strings.TrimRight(envOr("WEBD_APIKEYD_URL", "http://apikeyd:8889"), "/"),
		ExternalURL: strings.TrimRight(os.Getenv("WEBD_EXTERNAL_URL"), "/"),
	}

	if cfg.ExternalURL == "" {
		cfg.ExternalURL = "http://localhost:" + cfg.Port
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
