package consensus_test

import (
	"context"
	"testing"
	"time"

	"oracle/internal/consensus"
)

func TestPBFTConsensus(t *testing.T) {
	t.Log("Starting PBFT consensus test...")
	nodes := []string{"node1", "node2", "node3", "node4"}
	timeout := time.Second * 5

	t.Log("Creating PBFT instances...")
	// Create PBFT instances
	pbfts := make([]*consensus.PBFT, len(nodes))
	for i, nodeID := range nodes {
		pbft := consensus.NewPBFT(nodeID, nodes, timeout)
		pbfts[i] = pbft
		t.Logf("Created PBFT instance for node%d", i+1)

		// Verify initial leader state
		if i == 0 {
			if !pbft.IsLeader() {
				t.Fatalf("Expected node1 to be the leader")
			}
			t.Log("Confirmed node1 is the leader")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Log("Starting all PBFT nodes...")
	// Start all nodes
	for i, pbft := range pbfts {
		if err := pbft.Start(ctx); err != nil {
			t.Fatalf("Failed to start PBFT: %v", err)
		}
		t.Logf("Started node%d", i+1)
	}

	leaderPBFT := pbfts[0]

	t.Log("Proposing test value...")
	testValue := []byte("test value")
	err := leaderPBFT.ProposeValue(testValue)
	if err != nil {
		t.Fatalf("Failed to propose value: %v", err)
	}
	t.Log("Successfully proposed value")

	t.Log("Waiting for consensus...")
	time.Sleep(timeout)

	if !leaderPBFT.IsLeader() {
		t.Fatal("Leader lost leadership during consensus")
	}
	t.Log("Consensus test completed successfully")
}
