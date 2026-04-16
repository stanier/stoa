package main

import (
	"context"
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
	baseURL      := flag.String("base-url", "", "Netbox base URL, e.g. https://netbox.example.com (required)")
	token        := flag.String("token", "", "Netbox API token (required)")
	timeoutSecs  := flag.Int("timeout", 30, "HTTP timeout seconds")
	logFile      := flag.String("log", "", "Log file path (default: stderr)")
	pingTimeoutS := flag.Int("ping-timeout", 0, "Ping watchdog seconds, 0=disabled")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
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
	if err := mcpserver.RunServer(context.Background(), srv, time.Duration(*pingTimeoutS)*time.Second); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
