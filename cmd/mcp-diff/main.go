package main

import (
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
	f             := mcpserver.RegisterCommonFlags()
	maxInputBytes := flag.Int64("max-input-bytes", 512*1024, "Maximum input size per string/file (bytes)")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Max input size: %d bytes", *maxInputBytes)

	mcpserver.Serve(diff.NewServer(serverName, serverVersion, *maxInputBytes), f)
}
