package main

import (
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
	f       := mcpserver.RegisterCommonFlags()
	rootDir := flag.String("root", ".", "Root directory for filesystem operations")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Root directory: %s", *rootDir)

	fsServer, err := filesystem.NewServer(*rootDir, serverName, serverVersion)
	if err != nil {
		log.Fatalf("Failed to create filesystem server: %v", err)
	}
	mcpserver.Serve(fsServer, f)
}
