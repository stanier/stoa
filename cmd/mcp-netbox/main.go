package main

import (
	"flag"
	"log"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/netbox"
)

const (
	serverName    = "mcp-netbox"
	serverVersion = "0.1.0"
)

func main() {
	f            := mcpserver.RegisterCommonFlags()
	baseURL      := flag.String("base-url", "", "Netbox base URL, e.g. https://netbox.example.com (required)")
	token        := flag.String("token", "", "Netbox API token (required)")
	timeoutSecs  := flag.Int("timeout", 30, "HTTP timeout seconds")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	if *baseURL == "" {
		log.Fatal("-base-url is required")
	}
	if *token == "" {
		log.Fatal("-token is required")
	}
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Netbox URL: %s, timeout: %ds", *baseURL, *timeoutSecs)

	cfg := netbox.Config{
		BaseURL: *baseURL,
		Token:   *token,
		Timeout: time.Duration(*timeoutSecs) * time.Second,
	}
	srv, err := netbox.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create Netbox server: %v", err)
	}
	mcpserver.Serve(srv, f)
}
