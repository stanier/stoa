package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/stanier/stoa/internal/chat"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/trace"
	"github.com/stanier/stoa/internal/ui"
	"github.com/stanier/stoa/internal/version"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Parse command-line flags
	logLevel := flag.String("log-level", "", "Logging level: debug, info, warn, error (overrides config)")
	logFile := flag.String("log-file", "", "Log file path (overrides config)")
	enableConnectors := flag.Bool("enable-connectors", false, "Enable connectors (Zulip, etc.) even if configured")
	resumePath := flag.String("resume", "", "Path to a conversation trace file to resume")
	helpFlag := flag.Bool("help", false, "Show help message")
	versionFlag := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.Version)
		os.Exit(0)
	}

	if *helpFlag {
		fmt.Println("Usage: chat [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading configuration: %v\n", err)
		fmt.Println("\nMake sure to set OPENAI_API_KEY environment variable.")
		fmt.Println("You can also create a config file at ~/.config/stoa/config.yaml")
		os.Exit(1)
	}

	// Override logging config from command-line flags
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}
	if *logFile != "" {
		cfg.Logging.LogFile = *logFile
	}

	// Set runtime flag for connectors
	cfg.EnableConnectorsFlag = *enableConnectors

	// Initialize logging
	// DisableStderr prevents debug output from appearing in the TUI
	if err := logging.Init(logging.Config{
		Level:         logging.LogLevel(cfg.Logging.Level),
		LogFile:       cfg.Logging.LogFile,
		Pretty:        true,
		DisableStderr: true, // TUI mode - only log to file
	}); err != nil {
		fmt.Printf("Error initializing logging: %v\n", err)
		os.Exit(1)
	}

	logging.Info("Chat application starting")

	// Load resumed messages if requested
	var initialMessages []chat.Message
	if *resumePath != "" {
		var err error
		initialMessages, err = trace.LoadTrace(*resumePath)
		if err != nil {
			fmt.Printf("Error loading trace file: %v\n", err)
			os.Exit(1)
		}
		logging.Infof("Resuming session from %s (%d messages)", *resumePath, len(initialMessages))
	}

	// Create the UI model.
	// When AGENT_ADDRESS is set, always use the thin remote client regardless of
	// whether an LLM key is also present. An explicit agent address is a clear
	// signal of intent; the LLM key is irrelevant in that case.
	var model ui.Model
	if cfg.Agent.Address != "" {
		var err error
		model, err = ui.NewRemote(cfg)
		if err != nil {
			fmt.Printf("Error creating remote model: %v\n", err)
			os.Exit(1)
		}
	} else if len(initialMessages) > 0 {
		model = ui.NewResumed(cfg, initialMessages)
	} else {
		model = ui.NewWithConfig(cfg)
	}

	// Create the bubbletea program
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),       // Use alternate screen buffer
		tea.WithMouseCellMotion(), // Enable mouse support
		tea.WithReportFocus(),     // Enable focus/blur events
	)

	// Run the program
	if _, err := p.Run(); err != nil {
		logging.Errorf("Error running program: %v", err)
		os.Exit(1)
	}
}
