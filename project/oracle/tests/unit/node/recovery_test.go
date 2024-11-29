package node_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"oracle/internal/logging"
	"oracle/internal/node"
)

func TestRecovery(t *testing.T) {
	// Create temporary directory for state file
	tmpDir, err := os.MkdirTemp("", "oracle-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	logger := logging.NewLogger(logging.DEBUG, nil, "test")

	// Create test node
	n, err := node.NewOracleNode("test-node", "localhost:8000", logger)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Initialize consensus
	nodes := []string{"test-node", "peer1", "peer2"}
	n.InitConsensus(nodes)

	// Create recovery
	recovery := node.NewRecovery(n, statePath, logger)

	// Test saving state
	if err := recovery.SaveState(); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify state file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("State file was not created")
	}

	// Test recovery
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := recovery.Recover(ctx); err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
}
