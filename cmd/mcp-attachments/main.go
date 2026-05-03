package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/attachments"
)

const (
	serverName    = "mcp-attachments"
	serverVersion = "0.1.0"
)

func main() {
	f := mcpserver.RegisterCommonFlags()
	flag.Parse()

	mcpserver.SetupLogging(*f.LogFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	secret := os.Getenv("ATTACHMENT_SECRET")
	if secret == "" {
		log.Fatal("ATTACHMENT_SECRET environment variable is required")
	}

	cfg := attachments.Config{
		Secret:        []byte(secret),
		S3Endpoint:    strings.TrimRight(os.Getenv("S3_ENDPOINT"), "/"),
		S3Region:      os.Getenv("S3_REGION"),
		S3Bucket:      os.Getenv("S3_BUCKET"),
		S3AccessKeyID: os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretKey:   os.Getenv("S3_SECRET_KEY"),
	}

	srv, err := attachments.New(serverName, serverVersion, cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	mcpserver.Serve(srv, f)
}
