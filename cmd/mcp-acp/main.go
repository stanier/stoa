package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/acpclient"
)

const (
	serverName    = "mcp-acp"
	serverVersion = "0.1.0"
)

func main() {
	f               := mcpserver.RegisterCommonFlags()
	defaultEndpoint := flag.String("endpoint", envStr("ACP_ENDPOINT", ""), "Default ACP base URL (overridden per-call)")
	apiKey          := flag.String("api-key", envStr("ACP_API_KEY", ""), "Bearer token for authenticated ACP endpoints")
	timeoutSecs     := flag.Int("timeout", envInt("ACP_TIMEOUT", 120), "HTTP request timeout in seconds")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	if *defaultEndpoint != "" {
		log.Printf("Default ACP endpoint: %s", *defaultEndpoint)
	} else {
		log.Println("No default ACP endpoint configured (endpoint required per-call)")
	}
	log.Printf("Timeout: %ds", *timeoutSecs)

	cfg := acpclient.Config{
		DefaultEndpoint: *defaultEndpoint,
		APIKey:          *apiKey,
		TimeoutSecs:     *timeoutSecs,
	}
	mcpserver.Serve(acpclient.NewServer(serverName, serverVersion, cfg), f)
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
