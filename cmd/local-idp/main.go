package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/stanier/stoa/internal/localidp"
	"github.com/stanier/stoa/internal/logging"
)

const defaultPort = "9999"

func main() {
	port         := flag.String("port", defaultPort, "HTTP listen port")
	issuer       := flag.String("issuer", "", "Public base URL (env: LOCAL_IDP_ISSUER, e.g. http://local-idp:9999)")
	clientID     := flag.String("client-id", "", "OAuth2 client ID that authd will use (env: LOCAL_IDP_CLIENT_ID)")
	redirectURIs := flag.String("redirect-uris", "", "Comma-separated allowed redirect URIs (env: LOCAL_IDP_REDIRECT_URIS)")
	logLevel     := flag.String("log-level", "", "Log level: debug, info, warn, error")
	logFile      := flag.String("log-file", "", "Log file path")
	migrateUp    := flag.Bool("migrate", false, "Run database migrations and exit")
	helpFlag     := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *helpFlag {
		fmt.Println("local-idp — local OIDC identity provider for stoa")
		fmt.Println("\nUsage: local-idp [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nEndpoints:")
		fmt.Println("  GET  /.well-known/openid-configuration  — OIDC discovery document")
		fmt.Println("  GET  /oauth/authorize                   — login form + code issuance")
		fmt.Println("  POST /oauth/token                       — code → token exchange")
		fmt.Println("  GET  /oauth/userinfo                    — user claims")
		fmt.Println("  GET  /oauth/jwks                        — RS256 public key set")
		fmt.Println("  GET  /healthz                           — health check")
		fmt.Println("\nEnvironment Variables:")
		fmt.Println("  LOCAL_IDP_ISSUER        — public base URL (must match authd provider config)")
		fmt.Println("  LOCAL_IDP_CLIENT_ID     — OAuth2 client ID (default: stoa)")
		fmt.Println("  LOCAL_IDP_DATABASE_URL  — Postgres DSN")
		fmt.Println("  LOCAL_IDP_REDIRECT_URIS — comma-separated allowed redirect URIs (required)")
		os.Exit(0)
	}

	if err := logging.Init(logging.Config{
		Level:   logging.LogLevel(resolveStr(*logLevel, "info")),
		LogFile: *logFile,
		Pretty:  true,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "local-idp: failed to init logging: %v\n", err)
		os.Exit(1)
	}

	dsn := os.Getenv("LOCAL_IDP_DATABASE_URL")

	// Migration-only mode.
	if *migrateUp {
		if dsn == "" {
			fmt.Fprintln(os.Stderr, "local-idp: LOCAL_IDP_DATABASE_URL is required for migrations")
			os.Exit(1)
		}
		if err := localidp.Migrate(context.Background(), dsn); err != nil {
			fmt.Fprintf(os.Stderr, "local-idp: migration failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("local-idp: migrations complete")
		os.Exit(0)
	}

	resolvedIssuer := resolveStr(*issuer, os.Getenv("LOCAL_IDP_ISSUER"))
	if resolvedIssuer == "" {
		fmt.Fprintln(os.Stderr, "local-idp: LOCAL_IDP_ISSUER is required")
		os.Exit(1)
	}

	resolvedClientID := resolveStr(*clientID, os.Getenv("LOCAL_IDP_CLIENT_ID"))
	if resolvedClientID == "" {
		resolvedClientID = "stoa"
	}

	rawRedirectURIs := resolveStr(*redirectURIs, os.Getenv("LOCAL_IDP_REDIRECT_URIS"))
	if rawRedirectURIs == "" {
		fmt.Fprintln(os.Stderr, "local-idp: LOCAL_IDP_REDIRECT_URIS is required (comma-separated list of allowed redirect URIs)")
		os.Exit(1)
	}
	allowedURIs := strings.Split(rawRedirectURIs, ",")
	for i, u := range allowedURIs {
		allowedURIs[i] = strings.TrimSpace(u)
	}

	externalURL := resolveStr(os.Getenv("LOCAL_IDP_EXTERNAL_URL"), resolvedIssuer)

	srv, err := localidp.NewServer(context.Background(), resolvedIssuer, externalURL, resolvedClientID, dsn, allowedURIs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "local-idp: failed to start: %v\n", err)
		os.Exit(1)
	}

	addr := ":" + *port
	httpSrv := &http.Server{
		Addr:    addr,
		Handler: srv.Router(),
	}

	go func() {
		logging.Infof("local-idp: listening on %s (issuer: %s)", addr, resolvedIssuer)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Errorf("local-idp: server error: %v", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	logging.Info("local-idp: shutting down")
	_ = httpSrv.Close()
}

func resolveStr(flagVal, envVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return envVal
}
