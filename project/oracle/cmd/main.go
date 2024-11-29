package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"oracle/internal/config"
	"oracle/internal/node"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create and start oracle node
	oracleNode, err := node.NewOracleNode(cfg.NodeID, cfg.Peers)
	if err != nil {
		log.Fatalf("Failed to create oracle node: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := oracleNode.Start(ctx); err != nil {
		log.Fatalf("Failed to start oracle node: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutting down...")

	if err := oracleNode.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
