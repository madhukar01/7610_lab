package node_test

import (
	"context"
	"testing"
	"time"

	"oracle/internal/logging"
	"oracle/internal/node"
)

func TestOracleNode(t *testing.T) {
	logger := logging.NewLogger(logging.DEBUG, nil, "test")

	// Create node
	n, err := node.NewOracleNode("node1", "localhost:8000", logger)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Initialize consensus
	nodes := []string{"node1", "node2", "node3"}
	n.InitConsensus(nodes)

	// Start node
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := n.Start(ctx); err != nil {
		t.Fatalf("Failed to start node: %v", err)
	}

	// Stop node
	if err := n.Stop(); err != nil {
		t.Fatalf("Failed to stop node: %v", err)
	}
}
