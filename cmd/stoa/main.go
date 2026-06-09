package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stanier/stoa/internal/agentd"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/ui"
	"github.com/stanier/stoa/internal/version"
)

func main() {
	logLevel   := flag.String("log-level", "", "Log level: debug, info, warn, error")
	logFile    := flag.String("log-file", "", "Log file path")
	helpFlag   := flag.Bool("help", false, "Show help")
	versionFlag := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.Version)
		os.Exit(0)
	}

	if *helpFlag {
		fmt.Println("stoa — standalone AI agent with embedded agentd")
		fmt.Println("\nUsage: stoa [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nConfig: ~/.config/stoa/config.yaml")
		fmt.Println("Traces: ~/.config/stoa/traces/")
		fmt.Println("Sessions: set agentd.session_dir in config.yaml for persistence")
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stoa: failed to load config: %v\n", err)
		os.Exit(1)
	}
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}
	if *logFile != "" {
		cfg.Logging.LogFile = *logFile
	}

	// Try to initialise logging with the configured log file. If that fails
	// (e.g. the configured path is a container path not available locally),
	// fall back to the user's stoa config dir so the binary still starts.
	logCfg := logging.Config{
		Level:         logging.LogLevel(cfg.Logging.Level),
		LogFile:       cfg.Logging.LogFile,
		Pretty:        true,
		DisableStderr: true,
	}
	if err := logging.Init(logCfg); err != nil {
		configDir, _ := config.GetConfigDir()
		logCfg.LogFile = configDir + "/stoa.log"
		if err2 := logging.Init(logCfg); err2 != nil {
			// Last resort: no file logging, messages go nowhere (TUI hides stderr).
			logCfg.LogFile = ""
			_ = logging.Init(logCfg)
		}
	}

	if cfg.LLM.DefaultProvider() == nil {
		fmt.Fprintf(os.Stderr, "stoa: no LLM provider configured.\n")
		fmt.Fprintf(os.Stderr, "  Config file: %s\n", configFileDesc(cfg.LoadedFrom))
		fmt.Fprintf(os.Stderr, "  Add an llm.providers entry to ~/.config/stoa/config.yaml and set the API key env var.\n")
		os.Exit(1)
	}

	dp := cfg.LLM.DefaultProvider()
	configFile := configFileDesc(cfg.LoadedFrom)
	fmt.Fprintf(os.Stderr, "stoa: config:   %s\n", configFile)
	fmt.Fprintf(os.Stderr, "stoa: provider: %s (%s)\n", dp.Name, dp.Type)
	fmt.Fprintf(os.Stderr, "stoa: endpoint: %s\n", dp.APIURL)
	if dp.APIKey == "" {
		fmt.Fprintf(os.Stderr, "stoa: WARNING: API key is empty — set %s\n", dp.APIKeyEnv)
	}
	fmt.Fprintln(os.Stderr)

	srv, err := agentd.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stoa: failed to start embedded agent: %v\n", err)
		os.Exit(1)
	}
	defer srv.Stop()

	client := agentd.NewLocalClient(srv)

	model, err := ui.NewWithLocalAgent(cfg, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stoa: failed to create UI: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithReportFocus(),
	)

	if _, err := p.Run(); err != nil {
		logging.Errorf("stoa: %v", err)
		os.Exit(1)
	}
}

func configFileDesc(path string) string {
	if path == "" {
		return "(none found — using defaults)"
	}
	return path
}
