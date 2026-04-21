package apikeyd

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for apikeyd.
type Config struct {
	Port        int
	ExternalURL string
	DatabaseURL string
	AuthdURL    string
	// AdminToken gates the /admin/... endpoints. Read from APIKEYD_ADMIN_TOKEN.
	// When empty, admin endpoints return 404.
	AdminToken string
}

// fileConfig is the shape of apikeyd.yaml.
type fileConfig struct {
	Port           int    `yaml:"port"`
	ExternalURL    string `yaml:"external_url"`
	AuthdURL       string `yaml:"authd_url"`
	DatabaseURLEnv string `yaml:"database_url_env"` // env var to read the DSN from
}

// LoadConfig builds a Config by reading an optional YAML file then applying
// environment variable overrides.  Path may be empty to skip the file.
//
// Environment variables (always take precedence over the file):
//
//	APIKEYD_PORT          listen port
//	APIKEYD_EXTERNAL_URL  public base URL
//	APIKEYD_DATABASE_URL  Postgres DSN  (or set database_url_env in the file)
//	APIKEYD_AUTHD_URL     authd base URL
func LoadConfig(path string) (*Config, error) {
	// Start from defaults.
	fc := fileConfig{
		Port:        8889,
		ExternalURL: "",
		AuthdURL:    "http://authd:8888",
	}

	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("apikeyd: open config %q: %w", path, err)
		}
		defer f.Close()
		if err := yaml.NewDecoder(f).Decode(&fc); err != nil {
			return nil, fmt.Errorf("apikeyd: parse config %q: %w", path, err)
		}
	}

	// Resolve database URL: env var APIKEYD_DATABASE_URL wins, then
	// whatever env var the file nominated via database_url_env.
	dbURL := os.Getenv("APIKEYD_DATABASE_URL")
	if dbURL == "" && fc.DatabaseURLEnv != "" {
		dbURL = os.Getenv(fc.DatabaseURLEnv)
	}

	// Environment variable overrides.
	port := fc.Port
	if v := os.Getenv("APIKEYD_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("APIKEYD_PORT: %w", err)
		}
		port = n
	}

	extURL := fc.ExternalURL
	if v := os.Getenv("APIKEYD_EXTERNAL_URL"); v != "" {
		extURL = v
	}
	if extURL == "" {
		extURL = fmt.Sprintf("http://localhost:%d", port)
	}

	authdURL := fc.AuthdURL
	if v := os.Getenv("APIKEYD_AUTHD_URL"); v != "" {
		authdURL = v
	}

	if dbURL == "" {
		return nil, fmt.Errorf("database URL is required (set APIKEYD_DATABASE_URL or database_url_env in config file)")
	}

	return &Config{
		Port:        port,
		ExternalURL: extURL,
		DatabaseURL: dbURL,
		AuthdURL:    authdURL,
		AdminToken:  os.Getenv("APIKEYD_ADMIN_TOKEN"),
	}, nil
}
