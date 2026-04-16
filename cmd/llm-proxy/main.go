// llm-proxy is a bidirectional LLM API proxy.
//
// It accepts requests in OpenAI or Anthropic format and routes them to
// configured upstream providers (which can also use either dialect).
//
// Configuration can be provided via a YAML config file (-config flag) or
// via environment variables for simple single-upstream setups:
//
//	PROXY_LISTEN             listen address (default ":8080")
//	PROXY_UPSTREAM_DIALECT   "openai" or "anthropic"
//	PROXY_UPSTREAM_URL       upstream API base URL
//	PROXY_UPSTREAM_KEY       upstream API key
//	PROXY_UPSTREAM_MODEL     default model for this upstream
//	PROXY_UPSTREAM_MAX_TOKENS default max tokens (default 4096)
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/stanier/stoa/internal/proxy"
	"gopkg.in/yaml.v3"
)

func main() {
	configFile := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv, err := proxy.NewServer(cfg)
	if err != nil {
		log.Fatalf("proxy init: %v", err)
	}

	addr := cfg.Listen
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("llm-proxy listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// loadConfig reads config from a YAML file if provided, otherwise falls back
// to environment variables for a single-upstream setup.
func loadConfig(path string) (proxy.Config, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return proxy.Config{}, err
		}
		defer f.Close()
		var cfg proxy.Config
		if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
			return proxy.Config{}, err
		}
		return cfg, nil
	}

	// Fall back to env vars for a quick single-upstream setup.
	dialect := envOr("PROXY_UPSTREAM_DIALECT", "openai")
	apiURL := envOr("PROXY_UPSTREAM_URL", "")
	apiKey := envOr("PROXY_UPSTREAM_KEY", "")
	model := envOr("PROXY_UPSTREAM_MODEL", "")
	maxTokens := envInt("PROXY_UPSTREAM_MAX_TOKENS", 4096)

	if apiURL == "" && apiKey == "" {
		log.Fatal("no config file and no PROXY_UPSTREAM_URL / PROXY_UPSTREAM_KEY set — use -config or set env vars")
	}

	return proxy.Config{
		Listen:     envOr("PROXY_LISTEN", ":8080"),
		APIKeydURL: envOr("PROXY_APIKEYD_URL", ""),
		Upstreams: []proxy.UpstreamConfig{
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
