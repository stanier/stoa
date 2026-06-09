package main

import (
	"flag"
	"log"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/promql"
)

const (
	serverName    = "mcp-promql"
	serverVersion = "0.1.0"
)

func main() {
	f            := mcpserver.RegisterCommonFlags()
	baseURL      := flag.String("base-url", "", "Prometheus API base URL, e.g. http://prometheus:9090 (required)")
	token        := flag.String("token", "", "Bearer token for authentication (optional)")
	username     := flag.String("username", "", "Basic auth username (optional)")
	password     := flag.String("password", "", "Basic auth password (optional)")
	orgID        := flag.String("org-id", "", "X-Scope-OrgID header for Mimir/Cortex multi-tenancy (optional)")
	timeoutSecs  := flag.Int("timeout", 30, "HTTP timeout in seconds")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	if *baseURL == "" {
		log.Fatal("-base-url is required (e.g. http://prometheus:9090)")
	}
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Prometheus base URL: %s, timeout: %ds", *baseURL, *timeoutSecs)
	if *orgID != "" {
		log.Printf("X-Scope-OrgID: %s", *orgID)
	}

	cfg := promql.Config{
		BaseURL:  *baseURL,
		Token:    *token,
		Username: *username,
		Password: *password,
		OrgID:    *orgID,
		Timeout:  time.Duration(*timeoutSecs) * time.Second,
	}
	mcpserver.Serve(promql.NewServer(serverName, serverVersion, cfg), f)
}
