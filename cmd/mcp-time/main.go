package main

import (
	"flag"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/timeserver"
)

const (
	serverName    = "mcp-time"
	serverVersion = "0.1.0"
)

func main() {
	f := mcpserver.RegisterCommonFlags()
	flag.Parse()
	mcpserver.Run(serverName, serverVersion, f, timeserver.NewServer(serverName, serverVersion))
}
