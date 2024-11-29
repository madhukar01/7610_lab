package integration

import (
	"testing"
	"time"

	"oracle/internal/types"
	"oracle/tests"
)

func TestBasicConsensus(t *testing.T) {
	// Create a 4-node cluster
	cluster := tests.NewTestCluster(4)
	err := cluster.Start()
	if err != nil {
		t.Fatalf("Failed to start test cluster: %v", err)
	}
	defer cluster.Stop()

	// Submit a request to the first node
	req := &types.OracleRequest{
		RequestID: "test-1",
		Query:     "test query",
		Callback:  "callback-addr",
	}

	err = cluster.Nodes[0].SubmitRequest(req)
	if err != nil {
		t.Fatalf("Failed to submit request: %v", err)
	}

	// Wait for consensus
	time.Sleep(time.Second * 5)

	// Verify all nodes have processed the request
	// TODO: Add verification logic
}

func TestViewChange(t *testing.T) {
	cluster := tests.NewTestCluster(4)
	err := cluster.Start()
	if err != nil {
		t.Fatalf("Failed to start test cluster: %v", err)
	}
	defer cluster.Stop()

	// Stop the leader node
	leaderNode := cluster.Nodes[0]
	leaderNode.Stop()

	// Submit a request to a non-leader node
	req := &types.OracleRequest{
		RequestID: "test-2",
		Query:     "test query",
		Callback:  "callback-addr",
	}

	err = cluster.Nodes[1].SubmitRequest(req)
	if err != nil {
		t.Fatalf("Failed to submit request: %v", err)
	}

	// Wait for view change and consensus
	time.Sleep(time.Second * 10)

	// Verify request was processed with new leader
	// TODO: Add verification logic
}
