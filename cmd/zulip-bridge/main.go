// Command zulip-bridge connects a Zulip workspace to an agentd server.
// It is a dumb protocol translator: Zulip messages are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Zulip messages/reactions.
//
// The agentd address is read from the config key "agent.address" (default
// "http://localhost:7777") or the AGENT_ADDRESS environment variable.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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
		fmt.Println("zulip-bridge — Zulip ↔ agentd protocol translator")
		fmt.Println()
		fmt.Println("Usage: zulip-bridge [options]")
		fmt.Println()
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Environment Variables:")
		fmt.Println("  AGENT_ADDRESS       - agentd server URL (default: http://localhost:7777)")
		fmt.Println("  ZULIP_ENABLED       - Enable Zulip integration (true/false)")
		fmt.Println("  ZULIP_SITE_URL      - Zulip site URL (e.g., https://chat.example.com)")
		fmt.Println("  ZULIP_EMAIL         - Bot email address")
		fmt.Println("  ZULIP_API_KEY       - Bot API key")
		fmt.Println()
		fmt.Println("Configuration:")
		fmt.Println("  Config file: ~/.config/stoa/config.yaml")
		fmt.Println()
		fmt.Println("Example:")
		fmt.Println("  AGENT_ADDRESS=http://localhost:7777 \\")
		fmt.Println("    ZULIP_ENABLED=true ZULIP_SITE_URL=https://chat.example.com \\")
		fmt.Println("    ZULIP_EMAIL=bot@example.com ZULIP_API_KEY=xxx \\")
		fmt.Println("    ./zulip-bridge")
		os.Exit(0)
	}

	// Load configuration. The bridge does not need LLM credentials itself —
	// config.Validate() accepts a missing API key when Agent.Address is set.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		fmt.Fprintln(os.Stderr, "Set AGENT_ADDRESS, ZULIP_ENABLED, ZULIP_SITE_URL, ZULIP_EMAIL and ZULIP_API_KEY.")
		os.Exit(1)
	}

	// Apply CLI overrides.
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}
	if *logFile != "" {
		cfg.Logging.LogFile = *logFile
	}

	// Initialize logging.
	if err := logging.Init(logging.Config{
		Level:   logging.LogLevel(cfg.Logging.Level),
		LogFile: cfg.Logging.LogFile,
		Pretty:  true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error initialising logging: %v\n", err)
		os.Exit(1)
	}

	// Validate Zulip config.
	if !cfg.Zulip.Enabled || cfg.Zulip.SiteURL == "" || cfg.Zulip.Email == "" || cfg.Zulip.APIKey == "" {
		logging.Error("Zulip is not configured. Set ZULIP_ENABLED=true, ZULIP_SITE_URL, ZULIP_EMAIL, ZULIP_API_KEY.")
		os.Exit(1)
	}

	agentAddr := cfg.Agent.Address
	if agentAddr == "" {
		agentAddr = "http://localhost:7777"
	}
	logging.Infof("zulip-bridge: agentd address = %s", agentAddr)

	// Build the Zulip channel.
	ch, err := channels.NewChannel(cfg)
	if err != nil {
		logging.Errorf("Failed to create Zulip channel: %v", err)
		os.Exit(1)
	}
	zulipCh, ok := ch.(*channels.ZulipChannel)
	if !ok {
		logging.Error("Channel is not a ZulipChannel — only Zulip is supported by zulip-bridge")
		os.Exit(1)
	}

	// Start Zulip channel (connects to the event queue).
	startCtx, startCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := zulipCh.Start(startCtx); err != nil {
		startCancel()
		logging.Errorf("Failed to start Zulip channel: %v", err)
		os.Exit(1)
	}
	startCancel()
	logging.Info("Zulip channel started")

	// Build agentd client — attach service token and TLS options if configured.
	agentClient := agentproto.NewWSClientWithOptions(agentAddr, cfg.Agent.APIKey, cfg.Agent.TLSSkipVerify)

	// Build and start the bridge.
	bridge := NewBridge(cfg, agentClient, zulipCh)

	connCtx, connCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := bridge.Start(connCtx); err != nil {
		connCancel()
		logging.Errorf("Failed to start bridge: %v", err)
		os.Exit(1)
	}
	connCancel()

	logging.Info("zulip-bridge running. Press Ctrl+C to stop.")

	// Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Info("Shutting down...")

	bridge.Stop()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := zulipCh.Stop(stopCtx); err != nil {
		logging.Warnf("Error stopping Zulip channel: %v", err)
	}

	logging.Info("zulip-bridge exited")
}
