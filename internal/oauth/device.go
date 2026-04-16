package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceCodeResponse is returned by the provider's device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"` // minimum polling interval in seconds
}

// TokenResponse is returned by the provider's token endpoint after successful authorization.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// deviceError is an OAuth2 error response from the token endpoint.
type deviceError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// ErrAuthorizationPending means the user has not yet authorized the device.
var ErrAuthorizationPending = fmt.Errorf("oauth: authorization pending")

// ErrSlowDown means the client is polling too fast and must increase interval.
var ErrSlowDown = fmt.Errorf("oauth: slow down")

// ErrAccessDenied means the user denied the authorization request.
var ErrAccessDenied = fmt.Errorf("oauth: access denied")

// ErrDeviceCodeExpired means the device code has expired without authorization.
var ErrDeviceCodeExpired = fmt.Errorf("oauth: device code expired")

// StartDeviceFlow initiates the Device Authorization Grant with the provider.
// Returns the device code response that the TUI displays to the user.
func (p *Provider) StartDeviceFlow(ctx context.Context) (*DeviceCodeResponse, error) {
	data := url.Values{}
	data.Set("client_id", p.ClientID)
	data.Set("scope", "openid email profile")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.DeviceAuthEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build device auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: device auth request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: device auth returned HTTP %d: %s", resp.StatusCode, body)
	}

	var dc DeviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("oauth: decode device code response: %w", err)
	}
	if dc.DeviceCode == "" {
		return nil, fmt.Errorf("oauth: device auth response missing device_code")
	}
	if dc.Interval == 0 {
		dc.Interval = 5 // default per RFC 8628
	}
	return &dc, nil
}

// PollToken polls the provider's token endpoint for the given device code.
// Returns:
//   - (token, nil)             — user authorized, token issued
//   - (nil, ErrAuthorizationPending) — not yet authorized, try again later
//   - (nil, ErrSlowDown)       — must increase polling interval
//   - (nil, ErrAccessDenied)   — user denied the request
//   - (nil, ErrDeviceCodeExpired) — device code expired
//   - (nil, other error)       — unexpected failure
func (p *Provider) PollToken(ctx context.Context, deviceCode string) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	data.Set("device_code", deviceCode)
	data.Set("client_id", p.ClientID)
	data.Set("client_secret", p.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint,
		strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build token poll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token poll request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		var tok TokenResponse
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, fmt.Errorf("oauth: decode token response: %w", err)
		}
		if tok.AccessToken == "" {
			return nil, fmt.Errorf("oauth: token response missing access_token")
		}
		return &tok, nil
	}

	// Parse the error code from the response body.
	var oauthErr deviceError
	_ = json.Unmarshal(body, &oauthErr)

	switch oauthErr.Error {
	case "authorization_pending":
		return nil, ErrAuthorizationPending
	case "slow_down":
		return nil, ErrSlowDown
	case "access_denied":
		return nil, ErrAccessDenied
	case "expired_token":
		return nil, ErrDeviceCodeExpired
	default:
		return nil, fmt.Errorf("oauth: token endpoint error %q: %s", oauthErr.Error, oauthErr.ErrorDescription)
	}
}
