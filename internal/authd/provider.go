package authd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// provider holds the discovered OIDC endpoints for one upstream identity provider.
type provider struct {
	id           string
	issuerURL    string
	clientID     string
	clientSecret string

	// Discovered from /.well-known/openid-configuration
	authorizationEndpoint string
	tokenEndpoint         string
	userinfoEndpoint      string
}

// oidcDiscovery is the relevant subset of the OIDC discovery document.
type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

// discoverProvider fetches the OIDC discovery document for cfg and returns a
// fully-populated provider.
func discoverProvider(ctx context.Context, cfg providerConfig) (*provider, error) {
	secret := cfg.resolveSecret()
	if secret == "" {
		envName := "AUTHD_OAUTH_" + strings.ToUpper(strings.ReplaceAll(cfg.ID, "-", "_")) + "_CLIENT_SECRET"
		return nil, fmt.Errorf("authd: no client secret for provider %q — set %s", cfg.ID, envName)
	}

	discoveryURL := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("authd: build discovery request for %q: %w", cfg.ID, err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authd: fetch discovery document for %q: %w", cfg.ID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authd: discovery document for %q returned HTTP %d", cfg.ID, resp.StatusCode)
	}

	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("authd: decode discovery document for %q: %w", cfg.ID, err)
	}
	if doc.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("authd: provider %q missing authorization_endpoint in discovery document", cfg.ID)
	}
	if doc.TokenEndpoint == "" {
		return nil, fmt.Errorf("authd: provider %q missing token_endpoint in discovery document", cfg.ID)
	}
	if doc.UserinfoEndpoint == "" {
		return nil, fmt.Errorf("authd: provider %q missing userinfo_endpoint in discovery document — cannot fetch user identity", cfg.ID)
	}

	return &provider{
		id:                    cfg.ID,
		issuerURL:             cfg.IssuerURL,
		clientID:              cfg.ClientID,
		clientSecret:          secret,
		authorizationEndpoint: doc.AuthorizationEndpoint,
		tokenEndpoint:         doc.TokenEndpoint,
		userinfoEndpoint:      doc.UserinfoEndpoint,
	}, nil
}

// authorizationURL builds the upstream authorization URL with PKCE and state.
func (p *provider) authorizationURL(redirectURI, state, codeChallenge string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", p.clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	return p.authorizationEndpoint + "?" + params.Encode()
}

// tokenResponse is the response from the upstream token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

// exchangeCode exchanges an authorization code for an access token using PKCE.
func (p *provider) exchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*tokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", redirectURI)
	data.Set("client_id", p.clientID)
	data.Set("client_secret", p.clientSecret)
	data.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.tokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("authd: build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authd: token exchange request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("authd: token exchange returned HTTP %d: %s — %s",
			resp.StatusCode, errBody["error"], errBody["error_description"])
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("authd: decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("authd: token response missing access_token")
	}
	return &tok, nil
}

// userinfo holds the claims returned by the OIDC userinfo endpoint.
type userinfo struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

// fetchUserinfo calls the provider's userinfo endpoint with the access token.
func (p *provider) fetchUserinfo(ctx context.Context, accessToken string) (*userinfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userinfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("authd: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authd: fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authd: userinfo returned HTTP %d", resp.StatusCode)
	}

	var info userinfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("authd: decode userinfo: %w", err)
	}
	if info.Sub == "" {
		return nil, fmt.Errorf("authd: userinfo missing 'sub' claim")
	}
	return &info, nil
}
