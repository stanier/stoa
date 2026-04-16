package main

import (
	"context"
	"flag"
	"log"

	"github.com/stanier/stoa/internal/mcpserver"
	"github.com/stanier/stoa/internal/mcpserver/knowledgebase"
)

const (
	serverName    = "mcp-knowledgebase"
	serverVersion = "0.1.0"
)

func main() {
	dir         := flag.String("dir", "", "Directory of Markdown documents to index (required)")
	indexPath   := flag.String("index", "", "Path to the persistent index file (default: <dir>/.kb-index.json)")
	embedURL    := flag.String("embed-url", "", "Base URL of an OpenAI-compatible embeddings API, e.g. http://localhost:11434 for Ollama (omit for BM25-only search)")
	embedModel  := flag.String("embed-model", "nomic-embed-text", "Embedding model name")
	embedAPIKey := flag.String("embed-api-key", "", "API key for the embedding endpoint (optional)")
	bm25Weight  := flag.Float64("bm25-weight", 0.3, "BM25 weight in hybrid scoring (0.0 = pure semantic, 1.0 = pure BM25)")
	topK        := flag.Int("top-k", 5, "Default number of search results to return")
	logFile     := flag.String("log", "", "Log file path (empty for stderr)")
	pingTimeout := flag.Duration("ping-timeout", 0, "Shut down if no ping received within this duration (0 = disabled)")
	flag.Parse()

	mcpserver.SetupLogging(*logFile)
	log.Printf("Starting %s v%s", serverName, serverVersion)

	if *dir == "" {
		log.Fatalf("-dir is required")
	}

	log.Printf("Knowledge dir: %s", *dir)
	if *embedURL != "" {
		log.Printf("Embedding: %s (model: %s)", *embedURL, *embedModel)
	} else {
		log.Printf("Embedding: disabled (BM25-only search)")
	}
	log.Printf("BM25 weight: %.2f, top-k: %d", *bm25Weight, *topK)

	cfg := knowledgebase.Config{
		RootDir:     *dir,
		IndexPath:   *indexPath,
		EmbedURL:    *embedURL,
		EmbedModel:  *embedModel,
		EmbedAPIKey: *embedAPIKey,
		BM25Weight:  *bm25Weight,
		TopK:        *topK,
		Name:        serverName,
		Version:     serverVersion,
	}

	srv, err := knowledgebase.NewServer(context.Background(), cfg)
	if err != nil {
		log.Fatalf("Failed to create knowledgebase server: %v", err)
	}

	if err := mcpserver.RunServer(context.Background(), srv, *pingTimeout); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
