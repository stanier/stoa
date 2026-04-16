// Command discord-bridge connects a Discord workspace to an agentd server.
// It is a dumb protocol translator: Discord DMs are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Discord messages/reactions.
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
		fmt.Println("discord-bridge — Discord ↔ agentd protocol translator")
		fmt.Println()
		fmt.Println("Usage: discord-bridge [options]")
		fmt.Println()
		fmt.Println("Options:")
		flag.PrintDefaults()
		fmt.Println()
		fmt.Println("Environment Variables:")
		fmt.Println("  AGENT_ADDRESS       - agentd server URL (default: http://localhost:7777)")
		fmt.Println("  DISCORD_ENABLED     - Enable Discord integration (true/false)")
		fmt.Println("  DISCORD_BOT_TOKEN   - Bot token (from Discord Developer Portal)")
		fmt.Println()
		fmt.Println("Configuration:")
		fmt.Println("  Config file: ~/.config/stoa/config.yaml")
		fmt.Println()
		fmt.Println("Example:")
		fmt.Println("  AGENT_ADDRESS=http://localhost:7777 \\")
		fmt.Println("    DISCORD_ENABLED=true DISCORD_BOT_TOKEN=MTk4... \\")
		fmt.Println("    ./discord-bridge")
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		fmt.Fprintln(os.Stderr, "Set AGENT_ADDRESS, DISCORD_ENABLED and DISCORD_BOT_TOKEN.")
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

	if !cfg.Discord.Enabled || cfg.Discord.BotToken == "" {
		logging.Error("Discord is not configured. Set DISCORD_ENABLED=true and DISCORD_BOT_TOKEN.")
		os.Exit(1)
	}

	agentAddr := cfg.Agent.Address
	if agentAddr == "" {
		agentAddr = "http://localhost:7777"
	}
	logging.Infof("discord-bridge: agentd address = %s", agentAddr)

	discordCh, err := channels.NewDiscordChannel(channels.DiscordConfig{
		BotToken: cfg.Discord.BotToken,
	})
	if err != nil {
		logging.Errorf("Failed to create Discord channel: %v", err)
		os.Exit(1)
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := discordCh.Start(startCtx); err != nil {
		startCancel()
		logging.Errorf("Failed to start Discord channel: %v", err)
		os.Exit(1)
	}
	startCancel()
	logging.Info("Discord channel started")

	agentClient := agentproto.NewWSClientWithOptions(agentAddr, cfg.Agent.APIKey, cfg.Agent.TLSSkipVerify)

	bridge := NewBridge(cfg, agentClient, discordCh)

	connCtx, connCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := bridge.Start(connCtx); err != nil {
		connCancel()
		logging.Errorf("Failed to start bridge: %v", err)
		os.Exit(1)
	}
	connCancel()

	logging.Info("discord-bridge running. Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Info("Shutting down...")

	bridge.Stop()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := discordCh.Stop(stopCtx); err != nil {
		logging.Warnf("Error stopping Discord channel: %v", err)
	}

	logging.Info("discord-bridge exited")
}
