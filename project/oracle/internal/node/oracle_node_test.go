package node_test

import (
	"context"
	"testing"
	"time"

	"oracle/tests"
)

func TestOracleNode(t *testing.T) {
	// Create test nodes
	nodes := []string{"node1", "node2", "node3"}
	n, err := tests.CreateTestNode("node1", nodes)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}

	// Start node
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := n.ProcessPrompt(ctx, "test-req", "test prompt"); err != nil {
		t.Fatalf("Failed to process prompt: %v", err)
	}

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Check request state
	state, err := n.GetRequestState("test-req")
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	if state.Status == "failed" {
		t.Error("Request processing failed")
	}
	if state.LLMResponse == nil {
		t.Error("No LLM response received")
	}
}
