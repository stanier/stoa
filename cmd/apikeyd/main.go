// apikeyd manages API keys for programmatic access to the LLM proxy.
//
// Users authenticate via the standard OIDC flow (authd + local-idp) to reach
// the web UI, where they can create, label, and revoke API keys.  The keys
// themselves are validated by llm-proxy via the /keys/verify endpoint.
//
// Flags:
//
//	-config   path to apikeyd.yaml (optional; env vars always override)
//
// Environment variables (override file values):
//
//	APIKEYD_PORT          listen port (default 8889)
//	APIKEYD_EXTERNAL_URL  public base URL
//	APIKEYD_DATABASE_URL  Postgres DSN
//	APIKEYD_AUTHD_URL     authd base URL (default http://authd:8888)
package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/apikeyd"
)

func main() {
	configFile  := flag.String("config", "", "path to apikeyd.yaml")
	migrateOnly := flag.Bool("migrate-only", false, "run database migrations and exit")
	flag.Parse()

	cfg, err := apikeyd.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("apikeyd: config: %v", err)
	}

	ctx := context.Background()
	db, err := apikeyd.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("apikeyd: db: %v", err)
	}
	defer db.Close()

	if *migrateOnly {
		log.Println("apikeyd: migrations complete")
		return
	}

	srv := apikeyd.NewServer(cfg, db)

	log.Printf("apikeyd listening on :%d (authd: %s)", cfg.Port, cfg.AuthdURL)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("apikeyd: server: %v", err)
	}
}
