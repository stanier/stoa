package authd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// authdConfig is the full configuration for authd, loaded from a YAML file.
type authdConfig struct {
	// Providers lists the upstream OAuth2/OIDC providers authd brokers.
	Providers []providerConfig `yaml:"providers"`

	// SessionTTL controls how long human user session tokens remain valid.
	// Default: 24h.
	SessionTTL time.Duration `yaml:"session_ttl"`

	// ServiceTokenTTL controls how long service account tokens remain valid.
	// Default: 720h (30 days). Rotate via auth-admin authd service create.
	ServiceTokenTTL time.Duration `yaml:"service_token_ttl"`
}

// providerConfig describes one upstream OAuth2/OIDC identity provider.
type providerConfig struct {
	// ID is a short stable identifier used in logs and the users table.
	// Example: "google", "gitlab-ce"
	ID string `yaml:"id"`

	// IssuerURL is the OIDC issuer base URL used for OIDC discovery.
	// Example: "https://accounts.google.com"
	IssuerURL string `yaml:"issuer_url"`

	// ClientID is the OAuth2 application client ID registered with the provider.
	ClientID string `yaml:"client_id"`

	// ClientSecretEnv names the environment variable holding the client secret.
	// Defaults to AUTHD_OAUTH_<UPPERCASE_ID>_CLIENT_SECRET.
	ClientSecretEnv string `yaml:"client_secret_env"`
}

// resolveSecret returns the client secret for a provider from env.
func (p providerConfig) resolveSecret() string {
	if p.ClientSecretEnv != "" {
		return os.Getenv(p.ClientSecretEnv)
	}
	key := "AUTHD_OAUTH_" + strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_")) + "_CLIENT_SECRET"
	return os.Getenv(key)
}

// loadConfig reads authd's YAML config from the path given by AUTHD_CONFIG,
// falling back to /etc/authd/config.yaml (the expected container mount point).
func loadConfig() (*authdConfig, error) {
	path := os.Getenv("AUTHD_CONFIG")
	if path == "" {
		path = "/etc/authd/config.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file is valid — providers can be added later via env.
			return &authdConfig{}, nil
		}
		return nil, fmt.Errorf("authd: read config %s: %w", path, err)
	}

	var cfg authdConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("authd: parse config %s: %w", path, err)
	}

	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.ServiceTokenTTL == 0 {
		cfg.ServiceTokenTTL = 720 * time.Hour // 30 days
	}

	return &cfg, nil
}
