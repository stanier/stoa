package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/diff"
)

const (
	serverName    = "mcp-diff"
	serverVersion = "0.1.0"
)

func main() {
	maxInputBytes := flag.Int64("max-input-bytes", 512*1024, "Maximum input size per string/file (bytes)")
	logFile       := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout   := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Max input size: %d bytes", *maxInputBytes)

	srv := diff.NewServer(serverName, serverVersion, *maxInputBytes)
	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
