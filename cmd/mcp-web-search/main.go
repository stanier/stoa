package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/websearch"
)

const (
	serverName    = "mcp-web-search"
	serverVersion = "0.1.0"
)

func main() {
	apiKey      := flag.String("api-key", "", "Brave Search API key (or set BRAVE_API_KEY env var)")
	maxResults  := flag.Int("max-results", 10, "Default number of results to return (1-20)")
	safeSearch  := flag.String("safe-search", "moderate", "Default safe search level: off, moderate, strict")
	timeoutSecs := flag.Int("timeout", 30, "HTTP request timeout in seconds")
	logFile     := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)

	key := *apiKey
	if key == "" {
		key = os.Getenv("BRAVE_API_KEY")
	}
	if key == "" {
		log.Fatal("Brave Search API key required: set -api-key flag or BRAVE_API_KEY environment variable")
	}

	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Max results: %d, safe search: %s, timeout: %ds", *maxResults, *safeSearch, *timeoutSecs)

	cfg := websearch.Config{
		APIKey:      key,
		MaxResults:  *maxResults,
		SafeSearch:  *safeSearch,
		TimeoutSecs: *timeoutSecs,
	}
	srv := websearch.NewServer(serverName, serverVersion, cfg)
	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
