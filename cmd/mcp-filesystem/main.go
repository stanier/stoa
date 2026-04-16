package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/filesystem"
)

const (
	serverName    = "mcp-filesystem"
	serverVersion = "0.1.0"
)

func main() {
	rootDir     := flag.String("root", ".", "Root directory for filesystem operations")
	logFile     := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Root directory: %s", *rootDir)

	fsServer, err := filesystem.NewServer(*rootDir, serverName, serverVersion)
	if err != nil {
		log.Fatalf("Failed to create filesystem server: %v", err)
	}

	if err := mcpserver.RunServer(context.Background(), fsServer, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
