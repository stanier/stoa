package mcpserver

import (
	"context"
	"flag"
	"log"
	"time"
)

// CommonFlags holds the flags registered by RegisterCommonFlags.
type CommonFlags struct {
	LogFile     *string
	PingTimeout *time.Duration
}

// RegisterCommonFlags registers the -log and -ping-timeout flags that every MCP
// server needs. Call before registering server-specific flags, then call flag.Parse().
func RegisterCommonFlags() *CommonFlags {
	return &CommonFlags{
		LogFile:     flag.String("log", "", "Log file path (empty for stderr)"),
		PingTimeout: flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)"),
	}
}

// Run sets up logging, emits the standard "Starting <name> v<version>" line, and
// runs the server until exit. Calls log.Fatalf on error. Use for servers with no
// custom startup logging between setup and serve.
func Run(name, version string, f *CommonFlags, srv Server) {
	SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", name, version)
	Serve(srv, f)
}

// Serve runs srv with the ping timeout from f, calling log.Fatalf on error.
// Use instead of Run when custom startup logging is needed before serving.
func Serve(srv Server, f *CommonFlags) {
	if err := RunServer(context.Background(), srv, *f.PingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
