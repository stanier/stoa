package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/stanier/stoa/internal/daemon"
	"github.com/stanier/stoa/internal/logging"
	"github.com/stanier/stoa/internal/webd"
)

func main() {
	logLevel := flag.String("log-level", "", "Log level: debug, info, warn, error")
	helpFlag := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpFlag {
		fmt.Println("webd — stoa web UI server")
		fmt.Println("\nUsage: webd [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nEnvironment Variables:")
		fmt.Println("  WEBD_PORT              — HTTP listen port (default: 3000)")
		fmt.Println("  WEBD_EXTERNAL_URL      — public URL browsers reach webd at")
		fmt.Println("  WEBD_AUTHD_URL         — internal authd URL (default: http://authd:8888)")
		fmt.Println("  WEBD_AGENTD_URL        — internal agentd URL (default: http://agentd:7777)")
		os.Exit(0)
	}

	if err := logging.Init(logging.Config{
		Level:  logging.LogLevel(daemon.ResolveStr(*logLevel, "info")),
		Pretty: true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "webd: failed to init logging: %v\n", err)
		os.Exit(1)
	}

	cfg, err := webd.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "webd: %v\n", err)
		os.Exit(1)
	}

	srv, err := webd.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webd: failed to start: %v\n", err)
		os.Exit(1)
	}

	addr := ":" + cfg.Port
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv.Router(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 620 * time.Second, // longer than the 600s proxy_read_timeout for WS
		IdleTimeout:  120 * time.Second,
	}
	logging.Infof("webd: listening on %s (external: %s)", addr, cfg.ExternalURL)
	daemon.ServeHTTP("webd", httpSrv, 60*time.Second, "", "")
}
