// inference-gateway is a bidirectional LLM API inferencegateway.
//
// It accepts requests in OpenAI or Anthropic format and routes them to
// configured upstream providers (which can also use either dialect).
//
// Configuration can be provided via a YAML config file (-config flag) or
// via environment variables for simple single-upstream setups:
//
//	INFGW_LISTEN             listen address (default ":8080")
//	INFGW_UPSTREAM_DIALECT   "openai" or "anthropic"
//	INFGW_UPSTREAM_URL       upstream API base URL
//	INFGW_UPSTREAM_KEY       upstream API key
//	INFGW_UPSTREAM_MODEL     default model for this upstream
//	INFGW_UPSTREAM_MAX_TOKENS default max tokens (default 4096)
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/stanier/stoa/internal/inferencegateway"
	"gopkg.in/yaml.v3"
)

func main() {
	configFile := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv, err := inferencegateway.NewServer(cfg)
	if err != nil {
		log.Fatalf("proxy init: %v", err)
	}

	if hooks := inferencegateway.WebhookHooks(cfg.Gateway.Webhook); len(hooks.PreRequest) > 0 || len(hooks.ResponseTransform) > 0 || hooks.StreamingTransform != nil {
		srv.WithHooks(hooks)
		log.Printf("webhook hooks registered (pre-request: %v, response-transform: %v, streaming-transform: %v)",
			cfg.Gateway.Webhook.PreRequestURL != "",
			cfg.Gateway.Webhook.ResponseTransformURL != "",
			cfg.Gateway.Webhook.StreamingTransformURL != "")
	}

	addr := cfg.Listen
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("inference-gateway listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// loadConfig reads config from a YAML file if provided, otherwise falls back
// to environment variables for a single-upstream setup.
func loadConfig(path string) (inferencegateway.Config, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return inferencegateway.Config{}, err
		}
		defer f.Close()
		var cfg inferencegateway.Config
		if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
			return inferencegateway.Config{}, err
		}
		return cfg, nil
	}

	// Fall back to env vars for a quick single-upstream setup.
	dialect := envOr("INFGW_UPSTREAM_DIALECT", "openai")
	apiURL := envOr("INFGW_UPSTREAM_URL", "")
	apiKey := envOr("INFGW_UPSTREAM_KEY", "")
	model := envOr("INFGW_UPSTREAM_MODEL", "")
	maxTokens := envInt("INFGW_UPSTREAM_MAX_TOKENS", 4096)

	if apiURL == "" && apiKey == "" {
		log.Fatal("no config file and no INFGW_UPSTREAM_URL / INFGW_UPSTREAM_KEY set — use -config or set env vars")
	}

	return inferencegateway.Config{
		Listen:     envOr("INFGW_LISTEN", ":8080"),
		APIKeydURL: envOr("INFGW_APIKEYD_URL", ""),
		Upstreams: []inferencegateway.UpstreamConfig{
			{
				Name:             "default",
				Dialect:          dialect,
				APIURL:           apiURL,
				APIKey:           apiKey,
				DefaultModel:     model,
				DefaultMaxTokens: maxTokens,
			},
		},
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return fallback
}
