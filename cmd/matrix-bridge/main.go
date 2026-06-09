// Command matrix-bridge connects a Matrix homeserver to an agentd server.
// It is a dumb protocol translator: Matrix DMs are forwarded to agentd
// via WebSocket, and agentd events (responses, tool notices, permission
// requests) are sent back as Matrix messages/reactions.
package main

import (
	"flag"
	"fmt"
	"path/filepath"

	"github.com/stanier/stoa/internal/agentproto"
	"github.com/stanier/stoa/internal/bridgerunner"
	"github.com/stanier/stoa/internal/channels"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
)

func main() {
	bridgerunner.Run("matrix-bridge", printHelp, setup)
}

func printHelp() {
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
}

func setup(cfg *config.Config, agentClient agentproto.Client) (channels.Channel, bridgerunner.Bridge, error) {
	if !cfg.Matrix.Enabled || cfg.Matrix.HomeserverURL == "" || cfg.Matrix.Username == "" {
		logging.Error("Matrix is not configured. Set MATRIX_ENABLED=true, MATRIX_HOMESERVER_URL, and MATRIX_USERNAME.")
		return nil, nil, fmt.Errorf("matrix not configured")
	}
	if cfg.Matrix.Password == "" && cfg.Matrix.AccessToken == "" {
		logging.Error("Matrix auth not configured. Set MATRIX_PASSWORD or MATRIX_ACCESS_TOKEN.")
		return nil, nil, fmt.Errorf("matrix auth not configured")
	}

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
		return nil, nil, fmt.Errorf("failed to create Matrix channel: %w", err)
	}

	return matrixCh, NewBridge(cfg, agentClient, matrixCh), nil
}
