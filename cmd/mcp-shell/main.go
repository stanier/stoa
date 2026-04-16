package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/shellserver"
)

const (
	serverName    = "mcp-shell"
	serverVersion = "0.1.0"
)

func main() {
	logFile        := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout    := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	workspaceRoot  := flag.String("workspace", "/workspace", "Workspace root directory")
	timeout        := flag.Int("timeout", 60, "Default command timeout in seconds")
	maxOutput      := flag.Int("max-output", 51200, "Max combined stdout+stderr bytes per command")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s (workspace=%s, timeout=%ds, max-output=%d)",
		serverName, serverVersion, *workspaceRoot, *timeout, *maxOutput)

	srv := shellserver.NewServer(
		serverName,
		serverVersion,
		shellserver.WithWorkspaceRoot(*workspaceRoot),
		shellserver.WithDefaultTimeout(*timeout),
		shellserver.WithMaxOutputBytes(*maxOutput),
	)
	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
