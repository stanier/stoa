package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/stanier/stoa/internal/authd"
	"github.com/stanier/stoa/internal/daemon"
	"github.com/stanier/stoa/internal/logging"
)

const defaultPort = "8888"

func main() {
	port        := flag.String("port", defaultPort, "HTTP listen port")
	externalURL := flag.String("external-url", "", "Public base URL for OAuth2 redirect URIs (env: AUTHD_EXTERNAL_URL)")
	logLevel    := flag.String("log-level", "", "Log level: debug, info, warn, error")
	logFile     := flag.String("log-file", "", "Log file path")
	helpFlag    := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpFlag {
		fmt.Println("authd — authentication daemon (OAuth2 identity broker)")
		fmt.Println("\nUsage: authd [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nEndpoints:")
		fmt.Println("  GET  /oauth/start           — redirect browser to upstream provider")
		fmt.Println("  GET  /oauth/callback         — upstream provider redirect target")
		fmt.Println("  POST /token/verify           — validate a session token")
		fmt.Println("  POST /token/service          — issue a service account token (admin)")
		fmt.Println("  GET  /.well-known/jwks.json  — JWKS for token verification")
		fmt.Println("  GET  /healthz                — health check")
		fmt.Println("\nEnvironment Variables:")
		fmt.Println("  AUTHD_EXTERNAL_URL          — public base URL (e.g. https://auth.example.com)")
		fmt.Println("  AUTHD_DATABASE_URL          — Postgres DSN")
		fmt.Println("  AUTHD_SIGNING_KEY           — HMAC signing secret for session tokens")
		fmt.Println("  AUTHD_ADMIN_TOKEN           — Bearer token for admin endpoints")
		fmt.Println("  AUTHD_OAUTH_<ID>_CLIENT_SECRET — client secret for provider <ID>")
		os.Exit(0)
	}

	logCfg := logging.Config{
		Level:  logging.LogLevel(daemon.ResolveStr(*logLevel, "info")),
		LogFile: *logFile,
		Pretty: true,
	}
	if err := logging.Init(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "authd: failed to init logging: %v\n", err)
		os.Exit(1)
	}

	extURL := daemon.ResolveStr(*externalURL, os.Getenv("AUTHD_EXTERNAL_URL"))
	if extURL == "" {
		logging.Warn("authd: AUTHD_EXTERNAL_URL not set — OAuth2 callbacks will not work")
	}

	srv, err := authd.NewServer(authd.Config{
		ExternalURL: extURL,
		Port:        *port,
	})
	if err != nil {
		logging.Errorf("authd: failed to create server: %v", err)
		os.Exit(1)
	}

	addr := ":" + *port
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv.Router(),
	}
	logging.Infof("authd: listening on %s (external: %s)", addr, extURL)
	daemon.ServeHTTP("authd", httpSrv, 10*time.Second, "", "")
}
