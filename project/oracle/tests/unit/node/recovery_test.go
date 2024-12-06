package node_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mhollas/7610/oracle/pkg/logging"
	"github.com/mhollas/7610/oracle/pkg/node"
	"github.com/mhollas/7610/oracle/tests"
)

func TestRecovery(t *testing.T) {
	// Create temporary directory for state file
	tmpDir, err := os.MkdirTemp("", "oracle-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	statePath := filepath.Join(tmpDir, "state.json")
	logger := logging.NewLogger(logging.DEBUG, os.Stdout, "test")

	// Create test node
	nodes := []string{"test-node", "peer1", "peer2"}
	testNode, err := tests.CreateTestNode("test-node", nodes)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Create recovery using the wrapped OracleNode
	recovery := node.NewRecovery(testNode.OracleNode, statePath, logger)

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
