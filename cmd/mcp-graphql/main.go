package main

import (
	"flag"
	"log"
	"time"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/graphql"
)

const (
	serverName    = "mcp-graphql"
	serverVersion = "0.1.0"
)

func main() {
	f               := mcpserver.RegisterCommonFlags()
	endpoint        := flag.String("endpoint", "", "GraphQL endpoint URL (required)")
	authHeader      := flag.String("auth", "", "Authorization header value (e.g. 'Bearer token')")
	allowMutations  := flag.Bool("allow-mutations", false, "Expose graphql_mutate tool (disabled by default)")
	allowIntrospect := flag.Bool("allow-introspect", true, "Expose graphql_introspect and graphql_describe_type tools")
	maxRespMB       := flag.Int("max-response-mb", 5, "Maximum response size in megabytes")
	timeoutSecs     := flag.Int("timeout", 30, "HTTP timeout in seconds")
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	if *endpoint == "" {
		log.Fatal("-endpoint is required")
	}
	log.Printf("Starting %s v%s", serverName, serverVersion)
	log.Printf("Endpoint: %s, mutations: %v, introspect: %v", *endpoint, *allowMutations, *allowIntrospect)

	headers := make(map[string]string)
	if *authHeader != "" {
		headers["Authorization"] = *authHeader
	}
	cfg := graphql.Config{
		Endpoint:         *endpoint,
		Headers:          headers,
		Timeout:          time.Duration(*timeoutSecs) * time.Second,
		AllowMutations:   *allowMutations,
		AllowIntrospect:  *allowIntrospect,
		MaxResponseBytes: int64(*maxRespMB) * 1024 * 1024,
	}
	mcpserver.Serve(graphql.NewServer(cfg), f)
}
