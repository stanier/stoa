// Package bridgerunner provides the standard startup/shutdown lifecycle for bridge binaries.
// Each bridge binary calls Run with its name, an optional help printer, and a setup function
// that handles platform-specific validation and construction. The runner owns flag parsing,
// config loading, logging init, agentd client creation, and signal handling.
package bridgerunner

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

const (
	defaultAgentAddr = "http://localhost:7777"
	startupTimeout   = 30 * time.Second
	shutdownTimeout  = 10 * time.Second
)

// Bridge is implemented by every platform bridge.
type Bridge interface {
	Start(ctx context.Context) error
	Stop()
}

// SetupFunc validates platform-specific config, creates the channel (un-started) and bridge,
// and returns them. The runner calls Start/Stop on both in the correct order.
type SetupFunc func(cfg *config.Config, agentClient agentproto.Client) (channels.Channel, Bridge, error)

// Run is the standard entry point for bridge binaries. It handles flags, config, logging,
// agentd client construction, channel/bridge lifecycle, and graceful shutdown.
// printHelp is called when --help is requested; it may call flag.PrintDefaults() freely.
func Run(name string, printHelp func(), setup SetupFunc) {
	logLevel := flag.String("log-level", "", "Logging level: debug, info, warn, error (overrides config)")
	logFile  := flag.String("log-file", "", "Log file path (overrides config)")
	helpFlag := flag.Bool("help", false, "Show help message")
	flag.Parse()

	if *helpFlag {
		if printHelp != nil {
			printHelp()
		}
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
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

	agentAddr := cfg.Agent.Address
	if agentAddr == "" {
		agentAddr = defaultAgentAddr
	}
	logging.Infof("%s: agentd address = %s", name, agentAddr)

	agentClient := agentproto.NewWSClientWithOptions(agentAddr, cfg.Agent.APIKey, cfg.Agent.TLSSkipVerify)

	ch, bridge, err := setup(cfg, agentClient)
	if err != nil {
		logging.Errorf("%s: setup failed: %v", name, err)
		os.Exit(1)
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := ch.Start(startCtx); err != nil {
		startCancel()
		logging.Errorf("%s: failed to start channel: %v", name, err)
		os.Exit(1)
	}
	startCancel()
	logging.Infof("%s: channel started", name)

	connCtx, connCancel := context.WithTimeout(context.Background(), startupTimeout)
	if err := bridge.Start(connCtx); err != nil {
		connCancel()
		logging.Errorf("%s: failed to start bridge: %v", name, err)
		os.Exit(1)
	}
	connCancel()

	logging.Infof("%s running. Press Ctrl+C to stop.", name)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Infof("%s: shutting down...", name)

	bridge.Stop()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer stopCancel()
	if err := ch.Stop(stopCtx); err != nil {
		logging.Warnf("%s: error stopping channel: %v", name, err)
	}

	logging.Infof("%s exited", name)
}
