package channels

import (
	"fmt"

	"github.com/stanier/stoa/internal/config"
)

// NewChannel creates a channel based on configuration
// Currently only supports Zulip, but designed to be extensible
func NewChannel(cfg *config.Config) (Channel, error) {
	// Determine which channel to create based on config
	// Priority: Zulip (only one implemented for now)

	if cfg.Zulip.Enabled {
		return newZulipFromConfig(cfg), nil
	}

	if cfg.Slack.Enabled {
		return newSlackFromConfig(cfg), nil
	}
	if cfg.Discord.Enabled {
		return newDiscordFromConfig(cfg)
	}
	if cfg.Matrix.Enabled {
		return newMatrixFromConfig(cfg)
	}

	return nil, fmt.Errorf("no channel configured (enable zulip, slack, discord, matrix, etc. in config)")
}

// newZulipFromConfig creates a Zulip channel from application config
func newZulipFromConfig(cfg *config.Config) Channel {
	return NewZulipChannel(ZulipConfig{
		SiteURL:         cfg.Zulip.SiteURL,
		Email:           cfg.Zulip.Email,
		APIKey:          cfg.Zulip.APIKey,
		EventTypes:      cfg.Zulip.EventTypes,
		UseReactions:    cfg.Zulip.UseReactions,
		EmergencyCutoff: cfg.Zulip.EmergencyCutoff,
		AllowedStreams:  cfg.Zulip.AllowedStreams,
	})
}

func newSlackFromConfig(cfg *config.Config) Channel {
	return NewSlackChannel(SlackConfig{
		BotToken: cfg.Slack.BotToken,
		AppToken: cfg.Slack.AppToken,
	})
}

func newDiscordFromConfig(cfg *config.Config) (Channel, error) {
	return NewDiscordChannel(DiscordConfig{
		BotToken: cfg.Discord.BotToken,
	})
}

func newMatrixFromConfig(cfg *config.Config) (Channel, error) {
	return NewMatrixChannel(MatrixConfig{
		HomeserverURL: cfg.Matrix.HomeserverURL,
		UserID:        cfg.Matrix.Username,
		AccessToken:   cfg.Matrix.AccessToken,
		Password:      cfg.Matrix.Password,
	})
}
