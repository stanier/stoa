// Package oauth implements the OAuth2 Device Authorization Grant flow (RFC 8628)
// and OIDC provider discovery for agentd's authentication system.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/stanier/stoa/internal/config"
)

// Provider holds the resolved configuration and discovered endpoints for one
// OAuth2/OIDC identity provider.
type Provider struct {
	ID           string
	IssuerURL    string
	ClientID     string
	ClientSecret string

	// Discovered endpoints (from /.well-known/openid-configuration)
	DeviceAuthEndpoint string
	TokenEndpoint      string
	UserinfoEndpoint   string
}

// oidcDiscovery is the subset of the OIDC discovery document we need.
type oidcDiscovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	UserinfoEndpoint            string `json:"userinfo_endpoint"`
}

// Userinfo holds the claims returned by the OIDC userinfo endpoint.
type Userinfo struct {
	Sub         string `json:"sub"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

// Discover fetches the OIDC discovery document for the provider's issuer URL
// and returns a fully-populated Provider. The client secret is read from the
// environment variable named in cfg.ClientSecretEnv (with sensible defaults).
func Discover(ctx context.Context, cfg config.OAuthProviderConfig) (*Provider, error) {
	secret := resolveSecret(cfg)
	if secret == "" {
		return nil, fmt.Errorf("oauth: no client secret for provider %q — set %s", cfg.ID, secretEnvName(cfg))
	}

	discoveryURL := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: build discovery request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetch discovery document from %s: %w", discoveryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: discovery document returned HTTP %d from %s", resp.StatusCode, discoveryURL)
	}

	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oauth: decode discovery document: %w", err)
	}

	if doc.DeviceAuthorizationEndpoint == "" {
		return nil, fmt.Errorf("oauth: provider %q does not advertise device_authorization_endpoint — ensure GitLab's Device Auth Grant is enabled", cfg.ID)
	}

	return &Provider{
		ID:                 cfg.ID,
		IssuerURL:          cfg.IssuerURL,
		ClientID:           cfg.ClientID,
		ClientSecret:       secret,
		DeviceAuthEndpoint: doc.DeviceAuthorizationEndpoint,
		TokenEndpoint:      doc.TokenEndpoint,
		UserinfoEndpoint:   doc.UserinfoEndpoint,
	}, nil
}

// FetchUserinfo calls the provider's userinfo endpoint with the given access token.
func (p *Provider) FetchUserinfo(ctx context.Context, accessToken string) (*Userinfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserinfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: userinfo returned HTTP %d", resp.StatusCode)
	}

	var info Userinfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("oauth: decode userinfo: %w", err)
	}
	if info.Sub == "" {
		return nil, fmt.Errorf("oauth: userinfo missing 'sub' claim")
	}
	return &info, nil
}

// resolveSecret reads the client secret from environment, preferring
// cfg.ClientSecretEnv if set, then AGENTD_OAUTH_<ID>_CLIENT_SECRET,
// then AGENTD_OAUTH_CLIENT_SECRET.
func resolveSecret(cfg config.OAuthProviderConfig) string {
	if cfg.ClientSecretEnv != "" {
		return os.Getenv(cfg.ClientSecretEnv)
	}
	// Per-provider env var: AGENTD_OAUTH_GITLAB_CE_CLIENT_SECRET
	perProvider := "AGENTD_OAUTH_" + strings.ToUpper(strings.ReplaceAll(cfg.ID, "-", "_")) + "_CLIENT_SECRET"
	if v := os.Getenv(perProvider); v != "" {
		return v
	}
	// Fallback for single-provider deployments
	return os.Getenv("AGENTD_OAUTH_CLIENT_SECRET")
}

func secretEnvName(cfg config.OAuthProviderConfig) string {
	if cfg.ClientSecretEnv != "" {
		return cfg.ClientSecretEnv
	}
	return "AGENTD_OAUTH_" + strings.ToUpper(strings.ReplaceAll(cfg.ID, "-", "_")) + "_CLIENT_SECRET"
}
