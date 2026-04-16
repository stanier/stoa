package mcpserver

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunServer wraps a Server implementation with the standard MCP startup sequence:
// StdioServer creation, optional ping watchdog, OS signal handling, and Run.
//
// Callers are responsible for configuring log output before calling RunServer.
// pingTimeout: if > 0, the server exits if no ping is received within this duration.
//
// Returns a non-nil error only for unexpected server failures; context cancellation
// (clean shutdown) is swallowed.
func RunServer(ctx context.Context, impl Server, pingTimeout time.Duration) error {
	srv := NewStdioServer(impl)
	if pingTimeout > 0 {
		log.Printf("Ping watchdog enabled: timeout=%v", pingTimeout)
		srv.SetPingTimeout(pingTimeout)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal: %v", sig)
		cancel()
	}()

	log.Println("Server ready, listening on stdin/stdout")
	if err := srv.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	log.Println("Server shutdown complete")
	return nil
}

// SetupLogging redirects log output to logFile if non-empty, otherwise uses stderr.
// stderr is the correct default for MCP servers — stdout carries JSON-RPC traffic.
func SetupLogging(logFile string) {
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("Failed to open log file: %v", err)
		}
		// Note: f is intentionally not closed here; it lives for the process lifetime.
		log.SetOutput(f)
	} else {
		log.SetOutput(os.Stderr)
	}
}
