package main

import (
	"flag"
	"log"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/httpserver"
)

const (
	serverName    = "mcp-http"
	serverVersion = "0.1.0"
)

func main() {
	f               := mcpserver.RegisterCommonFlags()
	allowedDomains  := flag.String("allow-domains", "", "Comma-separated list of allowed domains (empty = all allowed)")
	maxBytes        := flag.Int64("max-bytes", 1*1024*1024, "Maximum response size in bytes")
	timeoutSecs     := flag.Int("timeout", 30, "HTTP request timeout in seconds")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	var domains []string
	if *allowedDomains != "" {
		for _, d := range strings.Split(*allowedDomains, ",") {
			if d = strings.TrimSpace(d); d != "" {
				domains = append(domains, d)
			}
		}
		log.Printf("Allowed domains: %v", domains)
	} else {
		log.Println("Allowed domains: all")
	}
	log.Printf("Max response size: %d bytes, timeout: %ds", *maxBytes, *timeoutSecs)

	cfg := httpserver.Config{
		AllowedDomains: domains,
		MaxBytes:       *maxBytes,
		TimeoutSecs:    *timeoutSecs,
	}
	mcpserver.Serve(httpserver.NewServer(serverName, serverVersion, cfg), f)
}
