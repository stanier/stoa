package main

import (
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
	f           := mcpserver.RegisterCommonFlags()
	storagePath := flag.String("storage", "", "Path to memory storage file (default: ~/.config/stoa/memory.json)")
	maxEntries  := flag.Int("max-entries", 10000, "Maximum number of memory entries (0 for unlimited)")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
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
	mcpserver.Serve(memServer, f)
}
