package webd

import (
	"fmt"
	"os"
	"strings"
	"time"
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

	// S3 holds object storage config for file uploads.
	// When empty, uploads fall back to local filesystem (dev/single-node only).
	S3 S3Config

	// AttachmentSecret is the HMAC secret used to sign opaque attachment IDs
	// returned to the browser. The same secret must be configured in the
	// mcp-attachments MCP server so it can validate and resolve IDs back to S3 keys.
	// Required when S3 is configured.
	AttachmentSecret []byte

	// AutoEmbedAttachments controls whether image attachments are fetched and
	// inlined as native multimodal content before the message reaches agentd.
	// When false the agent receives a text note and must call a retrieval tool.
	// Default: true.
	AutoEmbedAttachments bool
}

// LoadConfig reads webd configuration from environment variables.
func LoadConfig() (*Config, error) {
	autoEmbed := os.Getenv("WEBD_AUTO_EMBED_ATTACHMENTS") != "false"
	cfg := &Config{
		Port:                 envOr("WEBD_PORT", "3000"),
		AuthdURL:             strings.TrimRight(envOr("WEBD_AUTHD_URL", "http://authd:8888"), "/"),
		AgentdURL:            strings.TrimRight(envOr("WEBD_AGENTD_URL", "http://agentd:7777"), "/"),
		APIKeydURL:           strings.TrimRight(envOr("WEBD_APIKEYD_URL", "http://apikeyd:8889"), "/"),
		ExternalURL:          strings.TrimRight(os.Getenv("WEBD_EXTERNAL_URL"), "/"),
		AutoEmbedAttachments: autoEmbed,
	}

	if cfg.ExternalURL == "" {
		cfg.ExternalURL = "http://localhost:" + cfg.Port
	}

	if endpoint := os.Getenv("WEBD_S3_ENDPOINT"); endpoint != "" {
		ttl := 7 * 24 * time.Hour
		if raw := os.Getenv("WEBD_S3_PRESIGN_TTL"); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil {
				ttl = d
			} else {
				return nil, fmt.Errorf("webd: invalid WEBD_S3_PRESIGN_TTL %q: %w", raw, err)
			}
		}
		cfg.S3 = S3Config{
			Endpoint:        strings.TrimRight(endpoint, "/"),
			Region:          envOr("WEBD_S3_REGION", "garage"),
			Bucket:          envOr("WEBD_S3_BUCKET", "uploads"),
			AccessKeyID:     os.Getenv("WEBD_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("WEBD_S3_SECRET_ACCESS_KEY"),
			PresignTTL:      ttl,
		}
		if secret := os.Getenv("ATTACHMENT_SECRET"); secret != "" {
			cfg.AttachmentSecret = []byte(secret)
		} else {
			return nil, fmt.Errorf("webd: ATTACHMENT_SECRET is required when S3 is configured")
		}
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
