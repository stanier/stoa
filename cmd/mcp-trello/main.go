package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/trello"
)

const (
	serverName    = "mcp-trello"
	serverVersion = "0.1.0"
)

func main() {
	f             := mcpserver.RegisterCommonFlags()
	apiKey        := flag.String("api-key", "", "Trello API key (or set TRELLO_API_KEY env var)")
	token         := flag.String("token", "", "Trello API token (or set TRELLO_TOKEN env var)")
	allowedBoards := flag.String("allowed-boards", "", "Comma-separated list of board IDs to allow (empty = allow all)")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	if *apiKey == "" {
		*apiKey = os.Getenv("TRELLO_API_KEY")
	}
	if *token == "" {
		*token = os.Getenv("TRELLO_TOKEN")
	}
	if *apiKey == "" || *token == "" {
		log.Fatal("-api-key and -token flags (or TRELLO_API_KEY/TRELLO_TOKEN env vars) are required")
	}

	cfg := trello.Config{APIKey: *apiKey, Token: *token}
	if *allowedBoards != "" {
		for _, id := range strings.Split(*allowedBoards, ",") {
			if id = strings.TrimSpace(id); id != "" {
				cfg.AllowedBoards = append(cfg.AllowedBoards, id)
			}
		}
		log.Printf("Board whitelist enabled: %v", cfg.AllowedBoards)
	}

	mcpserver.Serve(trello.NewServer(serverName, serverVersion, cfg), f)
}
