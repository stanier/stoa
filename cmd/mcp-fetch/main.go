package main

import (
	"context"
	"flag"
	"log"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/fetch"
)

const (
	serverName    = "mcp-fetch"
	serverVersion = "0.1.0"
)

func main() {
	allowedDomains := flag.String("allow-domains", "", "Comma-separated list of allowed domains (empty = all allowed)")
	allowedHosts   := flag.String("allow-hosts", "", "Comma-separated internal hostnames exempt from private-IP SSRF check")
	maxBytes       := flag.Int64("max-bytes", 1*1024*1024, "Maximum response size in bytes")
	timeoutSecs    := flag.Int("timeout", 30, "HTTP request timeout in seconds")
	userAgent      := flag.String("user-agent", "mcp-fetch/0.1.0", "User-Agent header for requests")
	logFile        := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout    := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
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

	var hosts []string
	if *allowedHosts != "" {
		for _, h := range strings.Split(*allowedHosts, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		log.Printf("Allowed internal hosts (private-IP bypass): %v", hosts)
	}
	log.Printf("Max response size: %d bytes, timeout: %ds", *maxBytes, *timeoutSecs)

	cfg := fetch.Config{
		AllowedDomains: domains,
		AllowedHosts:   hosts,
		MaxBytes:       *maxBytes,
		TimeoutSecs:    *timeoutSecs,
		UserAgent:      *userAgent,
	}
	srv := fetch.NewServer(serverName, serverVersion, cfg)
	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
