package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/memory"
)

const (
	serverName    = "mcp-memory"
	serverVersion = "0.1.0"
)

func main() {
	storagePath := flag.String("storage", "", "Path to memory storage file (default: ~/.config/stoa/memory.json)")
	maxEntries  := flag.Int("max-entries", 10000, "Maximum number of memory entries (0 for unlimited)")
	logFile     := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	if *storagePath != "" {
		log.Printf("Storage: %s", *storagePath)
	} else {
		log.Printf("Storage: default (~/.config/stoa/memory.json)")
	}
	if *maxEntries > 0 {
		log.Printf("Max entries: %d", *maxEntries)
	} else {
		log.Printf("Max entries: unlimited")
	}

	memServer, err := memory.NewServer(*storagePath, serverName, serverVersion, *maxEntries)
	if err != nil {
		log.Fatalf("Failed to create memory server: %v", err)
	}

	if err := mcpserver.RunServer(context.Background(), memServer, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
