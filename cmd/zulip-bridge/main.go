// Command zulip-bridge connects a Zulip workspace to an agentd server.
// It is a dumb protocol translator: Zulip messages are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Zulip messages/reactions.
//
// The agentd address is read from the config key "agent.address" (default
// "http://localhost:7777") or the AGENT_ADDRESS environment variable.
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
	bridgerunner.Run("zulip-bridge", printHelp, setup)
}

func printHelp() {
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
}

func setup(cfg *config.Config, agentClient agentproto.Client) (channels.Channel, bridgerunner.Bridge, error) {
	if !cfg.Zulip.Enabled || cfg.Zulip.SiteURL == "" || cfg.Zulip.Email == "" || cfg.Zulip.APIKey == "" {
		logging.Error("Zulip is not configured. Set ZULIP_ENABLED=true, ZULIP_SITE_URL, ZULIP_EMAIL, ZULIP_API_KEY.")
		return nil, nil, fmt.Errorf("zulip not configured")
	}

	ch, err := channels.NewChannel(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create Zulip channel: %w", err)
	}
	zulipCh, ok := ch.(*channels.ZulipChannel)
	if !ok {
		return nil, nil, fmt.Errorf("channel is not a ZulipChannel")
	}

	return zulipCh, NewBridge(cfg, agentClient, zulipCh), nil
}
