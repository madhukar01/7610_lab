package consensus_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/network"
)

func TestPBFTConsensus(t *testing.T) {
	t.Log("Starting PBFT consensus test...")
	nodes := []string{"node1", "node2", "node3", "node4"}
	timeout := time.Second * 5

	t.Log("Creating PBFT instances...")
	// Create PBFT instances
	pbfts := make([]*consensus.PBFT, len(nodes))
	transports := make([]network.Transport, len(nodes))

	// Create network transports and PBFT instances
	for i, nodeID := range nodes {
		// Create transport
		addr := fmt.Sprintf("localhost:%d", 9000+i)
		transport := network.NewP2PTransport(nodeID, addr)
		transports[i] = transport

		// Create PBFT instance
		pbft := consensus.NewPBFT(nodeID, nodes, timeout)
		pbfts[i] = pbft
		t.Logf("Created PBFT instance for node%d", i+1)

		// Set up network manager
		networkManager := consensus.NewNetworkManager(transport, pbft)
		pbft.SetNetworkManager(networkManager)

		// Verify initial leader state
		if i == 0 {
			if !pbft.IsLeader() {
				t.Fatalf("Expected node1 to be the leader")
			}
			t.Log("Confirmed node1 is the leader")
		}
	}

	// Register peers with each other
	for i, transport := range transports {
		for j, nodeID := range nodes {
			if i != j {
				addr := fmt.Sprintf("localhost:%d", 9000+j)
				if err := transport.RegisterPeer(nodeID, addr); err != nil {
					t.Fatalf("Failed to register peer: %v", err)
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Log("Starting all PBFT nodes...")
	// Start all nodes and transports
	for i := range nodes {
		if err := transports[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start transport: %v", err)
		}
		if err := pbfts[i].Start(ctx); err != nil {
			t.Fatalf("Failed to start PBFT: %v", err)
		}
		t.Logf("Started node%d", i+1)
	}

	// Wait for network connections
	time.Sleep(time.Second)

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

	// Clean up
	for i := range nodes {
		if err := pbfts[i].Stop(); err != nil {
			t.Logf("Warning: failed to stop PBFT: %v", err)
		}
		if err := transports[i].Stop(); err != nil {
			t.Logf("Warning: failed to stop transport: %v", err)
		}
	}
}
