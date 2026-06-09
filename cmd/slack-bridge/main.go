// Command slack-bridge connects a Slack workspace to an agentd server.
// It is a dumb protocol translator: Slack messages are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Slack messages/reactions.
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
	bridgerunner.Run("slack-bridge", printHelp, setup)
}

func printHelp() {
	fmt.Println("slack-bridge — Slack ↔ agentd protocol translator")
	fmt.Println()
	fmt.Println("Usage: slack-bridge [options]")
	fmt.Println()
	fmt.Println("Options:")
	flag.PrintDefaults()
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  AGENT_ADDRESS       - agentd server URL (default: http://localhost:7777)")
	fmt.Println("  SLACK_ENABLED       - Enable Slack integration (true/false)")
	fmt.Println("  SLACK_BOT_TOKEN     - Bot token (xoxb-...)")
	fmt.Println("  SLACK_APP_TOKEN     - App-level token for Socket Mode (xapp-...)")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Println("  Config file: ~/.config/stoa/config.yaml")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  AGENT_ADDRESS=http://localhost:7777 \\")
	fmt.Println("    SLACK_ENABLED=true SLACK_BOT_TOKEN=xoxb-... SLACK_APP_TOKEN=xapp-... \\")
	fmt.Println("    ./slack-bridge")
}

func setup(cfg *config.Config, agentClient agentproto.Client) (channels.Channel, bridgerunner.Bridge, error) {
	if !cfg.Slack.Enabled || cfg.Slack.BotToken == "" || cfg.Slack.AppToken == "" {
		logging.Error("Slack is not configured. Set SLACK_ENABLED=true, SLACK_BOT_TOKEN, SLACK_APP_TOKEN.")
		return nil, nil, fmt.Errorf("slack not configured")
	}

	slackCh := channels.NewSlackChannel(channels.SlackConfig{
		BotToken: cfg.Slack.BotToken,
		AppToken: cfg.Slack.AppToken,
	})

	return slackCh, NewBridge(cfg, agentClient, slackCh), nil
}
