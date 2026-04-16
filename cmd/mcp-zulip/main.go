package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/zulip"
)

const (
	serverName    = "mcp-zulip"
	serverVersion = "0.1.0"
)

func main() {
	baseURL       := flag.String("base-url", "", "Zulip server URL, e.g. https://yourorg.zulipchat.com (required)")
	email         := flag.String("email", "", "Bot email address (required)")
	apiKey        := flag.String("api-key", "", "Bot API key (required)")
	timeoutSecs   := flag.Int("timeout", 30, "HTTP timeout seconds")
	logFile       := flag.String("log", "", "Log file path (default: stderr)")
	pingTimeoutS  := flag.Int("ping-timeout", 0, "Ping watchdog timeout seconds, 0=disabled")
	attachmentDir := flag.String("attachment-dir", "/mcp/root/attachments", "Directory to save downloaded attachments")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	if *baseURL == "" {
		log.Fatal("-base-url is required (e.g. https://yourorg.zulipchat.com)")
	}
	if *email == "" {
		log.Fatal("-email is required")
	}
	if *apiKey == "" {
		log.Fatal("-api-key is required")
	}

	cfg := zulip.Config{
		BaseURL:       *baseURL,
		Email:         *email,
		APIKey:        *apiKey,
		Timeout:       time.Duration(*timeoutSecs) * time.Second,
		AttachmentDir: *attachmentDir,
	}
	log.Printf("Connecting to Zulip at %s as %s", cfg.BaseURL, cfg.Email)

	srv, err := zulip.NewServer(serverName, serverVersion, cfg)
	if err != nil {
		log.Fatalf("Failed to create Zulip server: %v", err)
	}
	if err := mcpserver.RunServer(context.Background(), srv, time.Duration(*pingTimeoutS)*time.Second); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
