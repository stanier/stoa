// Command discord-bridge connects a Discord workspace to an agentd server.
// It is a dumb protocol translator: Discord DMs are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Discord messages/reactions.
package main

import (
	"flag"
	"fmt"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridgerunner"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
)

func main() {
	bridgerunner.Run("discord-bridge", printHelp, setup)
}

func printHelp() {
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
}

func setup(cfg *config.Config, agentClient agentproto.Client) (channels.Channel, bridgerunner.Bridge, error) {
	if !cfg.Discord.Enabled || cfg.Discord.BotToken == "" {
		logging.Error("Discord is not configured. Set DISCORD_ENABLED=true and DISCORD_BOT_TOKEN.")
		return nil, nil, fmt.Errorf("discord not configured")
	}

	discordCh, err := channels.NewDiscordChannel(channels.DiscordConfig{
		BotToken: cfg.Discord.BotToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Discord channel: %w", err)
	}

	return discordCh, NewBridge(cfg, agentClient, discordCh), nil
}
