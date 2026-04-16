package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/timeserver"
)

const (
	serverName    = "mcp-time"
	serverVersion = "0.1.0"
)

func main() {
	logFile     := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	srv := timeserver.NewServer(serverName, serverVersion)
	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
