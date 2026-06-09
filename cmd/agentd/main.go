package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/stanier/stoa/internal/agentd"
	"github.com/stanier/stoa/internal/config"
	"github.com/stanier/stoa/internal/daemon"
	"github.com/stanier/stoa/internal/logging"
)

const defaultPort = "7777"

func main() {
	port     := flag.String("port", defaultPort, "HTTP listen port")
	logLevel := flag.String("log-level", "", "Log level: debug, info, warn, error")
	logFile  := flag.String("log-file", "", "Log file path")
	tlsFlag  := flag.Bool("tls", false, "Enable TLS (auto-generates a self-signed cert if --cert/--key are not provided)")
	certFile := flag.String("cert", "", "TLS certificate file (PEM)")
	keyFile  := flag.String("key", "", "TLS private key file (PEM)")
	helpFlag := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpFlag {
		fmt.Println("agentd — agent daemon WebSocket server")
		fmt.Println("\nUsage: agentd [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nEndpoints:")
		fmt.Println("  POST /sessions        — create a new session")
		fmt.Println("  GET  /sessions        — list all sessions")
		fmt.Println("  WS   /sessions/:id/ws — bidirectional WebSocket for a session")
		fmt.Println("\nEnvironment Variables:")
		fmt.Println("  ANTHROPIC_API_KEY  - Anthropic API key (for providers with type: anthropic in config.yaml)")
		fmt.Println("  OPENAI_API_KEY     - OpenAI API key (for providers with type: openai in config.yaml)")
		fmt.Println("  AGENTD_AUTHD_URL   - authd base URL for token validation (e.g. http://authd:8888)")
		fmt.Println("  AGENTD_NO_AUTH     - set to 'true' to run without auth (local dev only)")
		fmt.Println("\nNote: provider selection and model are set in config.yaml under llm.providers.")
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentd: failed to load config: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "agentd: failed to init logging: %v\n", err)
		os.Exit(1)
	}

	if err := agentd.CheckAuthConfig(os.Getenv("AGENTD_AUTHD_URL"), os.Getenv("AGENTD_NO_AUTH")); err != nil {
		logging.Errorf("agentd: %v", err)
		os.Exit(1)
	}

	// Resolve TLS cert/key paths.
	if *tlsFlag && *certFile == "" {
		configDir, _ := config.GetConfigDir()
		*certFile = filepath.Join(configDir, "agentd-cert.pem")
		*keyFile = filepath.Join(configDir, "agentd-key.pem")
	}
	if *tlsFlag {
		*certFile, *keyFile, err = agentd.EnsureSelfSignedCert(*certFile, *keyFile)
		if err != nil {
			logging.Errorf("agentd: TLS cert error: %v", err)
			os.Exit(1)
		}
	}

	srv, err := agentd.NewServer(cfg)
	if err != nil {
		logging.Errorf("agentd: failed to create server: %v", err)
		os.Exit(1)
	}

	handler := agentd.AuthMiddlewareWithAPIKeyd(srv.AuthdClient(), srv.ApikeydClient(), srv.Router())
	addr := ":" + *port
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	if *tlsFlag {
		logging.Infof("agentd: listening on %s (TLS)", addr)
	} else {
		logging.Infof("agentd: listening on %s (plaintext)", addr)
	}
	daemon.ServeHTTP("agentd", httpSrv, 60*time.Second, *certFile, *keyFile)
	srv.Stop()
}
