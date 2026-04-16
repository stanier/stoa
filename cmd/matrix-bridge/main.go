// Command matrix-bridge connects a Matrix homeserver to an agentd server.
// It is a dumb protocol translator: Matrix DMs are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Matrix messages/reactions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
)

const startupTimeout = 30 * time.Second

func main() {
	logLevel := flag.String("log-level", "", "Logging level: debug, info, warn, error (overrides config)")
	logFile := flag.String("log-file", "", "Log file path (overrides config)")
	helpFlag := flag.Bool("help", false, "Show help message")
	flag.Parse()

	if *helpFlag {
		fmt.Println("matrix-bridge — Matrix ↔ agentd protocol translator")
		fmt.Println()
		fmt.Println("Usage: matrix-bridge [options]")
		fmt.Println()
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Environment Variables:")
		fmt.Println("  AGENT_ADDRESS            - agentd server URL (default: http://localhost:7777)")
		fmt.Println("  MATRIX_ENABLED           - Enable Matrix integration (true/false)")
		fmt.Println("  MATRIX_HOMESERVER_URL    - Matrix homeserver URL (e.g. https://matrix.org)")
		fmt.Println("  MATRIX_USERNAME          - Bot username (e.g. @bot:server.com)")
		fmt.Println("  MATRIX_PASSWORD          - Bot password (if not using access token)")
		fmt.Println("  MATRIX_ACCESS_TOKEN      - Bot access token (alternative to password)")
		fmt.Println()
		fmt.Println("Configuration:")
		fmt.Println("  Config file: ~/.config/stoa/config.yaml")
		fmt.Println()
		fmt.Println("Example:")
		fmt.Println("  AGENT_ADDRESS=http://localhost:7777 \\")
		fmt.Println("    MATRIX_ENABLED=true MATRIX_HOMESERVER_URL=https://matrix.org \\")
		fmt.Println("    MATRIX_USERNAME=@bot:matrix.org MATRIX_PASSWORD=secret \\")
		fmt.Println("    ./matrix-bridge")
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		fmt.Fprintln(os.Stderr, "Set AGENT_ADDRESS, MATRIX_ENABLED, MATRIX_HOMESERVER_URL, and MATRIX_USERNAME.")
		os.Exit(1)
	}

	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}
	if *logFile != "" {
		cfg.Logging.LogFile = *logFile
	}

	if err := logging.Init(logging.Config{
		Level:   logging.LogLevel(cfg.Logging.Level),
		LogFile: cfg.Logging.LogFile,
		Pretty:  true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error initialising logging: %v\n", err)
		os.Exit(1)
	}

	if !cfg.Matrix.Enabled || cfg.Matrix.HomeserverURL == "" || cfg.Matrix.Username == "" {
		logging.Error("Matrix is not configured. Set MATRIX_ENABLED=true, MATRIX_HOMESERVER_URL, and MATRIX_USERNAME.")
		os.Exit(1)
	}
	if cfg.Matrix.Password == "" && cfg.Matrix.AccessToken == "" {
		logging.Error("Matrix auth not configured. Set MATRIX_PASSWORD or MATRIX_ACCESS_TOKEN.")
		os.Exit(1)
	}

	agentAddr := cfg.Agent.Address
	if agentAddr == "" {
		agentAddr = "http://localhost:7777"
	}
	logging.Infof("matrix-bridge: agentd address = %s", agentAddr)

	// Derive sync token path from session file path (same directory, fixed filename).
	syncTokenPath := ""
	if cfg.Matrix.SessionFile != "" {
		syncTokenPath = filepath.Join(filepath.Dir(cfg.Matrix.SessionFile), "matrix-sync-token")
	}

	matrixCh, err := channels.NewMatrixChannel(channels.MatrixConfig{
		HomeserverURL: cfg.Matrix.HomeserverURL,
		UserID:        cfg.Matrix.Username,
		AccessToken:   cfg.Matrix.AccessToken,
		Password:      cfg.Matrix.Password,
		SyncTokenPath: syncTokenPath,
	})
	if err != nil {
		logging.Errorf("Failed to create Matrix channel: %v", err)
		os.Exit(1)
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := matrixCh.Start(startCtx); err != nil {
		startCancel()
		logging.Errorf("Failed to start Matrix channel: %v", err)
		os.Exit(1)
	}
	startCancel()
	logging.Info("Matrix channel started")

	agentClient := agentproto.NewWSClientWithOptions(agentAddr, cfg.Agent.APIKey, cfg.Agent.TLSSkipVerify)

	bridge := NewBridge(cfg, agentClient, matrixCh)

	connCtx, connCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := bridge.Start(connCtx); err != nil {
		connCancel()
		logging.Errorf("Failed to start bridge: %v", err)
		os.Exit(1)
	}
	connCancel()

	logging.Info("matrix-bridge running. Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Info("Shutting down...")

	bridge.Stop()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := matrixCh.Stop(stopCtx); err != nil {
		logging.Warnf("Error stopping Matrix channel: %v", err)
	}

	logging.Info("matrix-bridge exited")
}
