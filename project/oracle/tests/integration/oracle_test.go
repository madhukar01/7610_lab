package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"agent/src/llm"
	"oracle/internal/consensus"
	"oracle/internal/logging"
	"oracle/internal/node"
	"oracle/internal/similarity"
	"oracle/internal/storage"
)

func TestOracleConsensus(t *testing.T) {
	// Create test nodes
	nodeIDs := []string{"node1", "node2", "node3", "node4"}
	nodes := make([]*node.OracleNode, len(nodeIDs))
	addresses := make(map[string]string)

	fmt.Println("\n=== Starting Oracle Consensus Test ===")
	fmt.Println("Creating", len(nodeIDs), "nodes...")

	// Create shared components
	cfg := &llm.Config{
		Model:              "gpt-4-1106-preview",
		MaxTokens:          1000,
		DefaultTemperature: 0.7,
		MaxRetries:         3,
		RetryIntervalMs:    1000,
	}

	// Initialize nodes
	for i, nodeID := range nodeIDs {
		// Create LLM client
		llmClient, err := llm.NewOpenAIClient("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", cfg)
		if err != nil {
			t.Fatalf("Failed to create LLM client: %v", err)
		}

		// Create storage client
		storageClient, err := storage.NewPinataClient(
			"08c14515d80ee5db9865",
			"fb3ed454a66f0615242d7711f0ba1d788c153b6afe47a8aaeef9f11ae179f253",
			"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VySW5mb3JtYXRpb24iOnsiaWQiOiI2ZjdiOGMxOS0yOGJhLTRlMGUtOGZkYi1jNjUyZGNkYWY2ZTMiLCJlbWFpbCI6Im1ob2xsYThAZ21haWwuY29tIiwiZW1haWxfdmVyaWZpZWQiOnRydWUsInBpbl9wb2xpY3kiOnsicmVnaW9ucyI6W3siZGVzaXJlZFJlcGxpY2F0aW9uQ291bnQiOjEsImlkIjoiRlJBMSJ9LHsiZGVzaXJlZFJlcGxpY2F0aW9uQ291bnQiOjEsImlkIjoiTllDMSJ9XSwidmVyc2lvbiI6MX0sIm1mYV9lbmFibGVkIjpmYWxzZSwic3RhdHVzIjoiQUNUSVZFIn0sImF1dGhlbnRpY2F0aW9uVHlwZSI6InNjb3BlZEtleSIsInNjb3BlZEtleUtleSI6IjA4YzE0NTE1ZDgwZWU1ZGI5ODY1Iiwic2NvcGVkS2V5U2VjcmV0IjoiZmIzZWQ0NTRhNjZmMDYxNTI0MmQ3NzExZjBiYTFkNzg4YzE1M2I2YWZlNDdhOGFhZWVmOWYxMWFlMTc5ZjI1MyIsImV4cCI6MTc2NDY2OTQzNX0.Pq8OfbhJF4CALIXO0pT3RyZAPBHQrbLU9p-qbq_3E4s",
		)
		if err != nil {
			t.Fatalf("Failed to create storage client: %v", err)
		}

		// Create semantic scorer
		scorer := similarity.NewSemanticScorer("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", "")

		// Create logger
		logger := logging.NewLogger(logging.DEBUG, nil, fmt.Sprintf("node-%s", nodeID))

		// Create node
		addr := fmt.Sprintf("localhost:%d", 8545+i)
		addresses[nodeID] = addr

		node, err := node.NewOracleNode(nodeID, addr, llmClient, storageClient, scorer, logger)
		if err != nil {
			t.Fatalf("Failed to create node %s: %v", nodeID, err)
		}

		// Initialize consensus
		node.InitConsensus(nodeIDs)
		nodes[i] = node
		fmt.Printf("Created node: %s at %s\n", nodeID, addr)
	}

	// Register peers with each other
	fmt.Println("\nRegistering peer connections...")
	for _, node := range nodes {
		for _, peerID := range nodeIDs {
			if peerID != node.GetNodeID() {
				peerAddr := addresses[peerID]
				if err := node.RegisterPeer(peerID, peerAddr); err != nil {
					t.Fatalf("Failed to register peer %s: %v", peerID, err)
				}
			}
		}
	}

	// Create message verification channels
	type pbftMessage struct {
		nodeID   string
		msgType  consensus.MessageType
		sequence uint64
	}
	msgChan := make(chan pbftMessage, 1000)

	// Add message verification callback to each node
	for _, n := range nodes {
		node := n // Create local copy for closure
		origCallback := node.GetMessageCallback()
		node.SetMessageCallback(func(msg *consensus.ConsensusMessage) {
			// Call original callback first
			if origCallback != nil {
				origCallback(msg)
			}
			// Send message info to verification channel
			msgChan <- pbftMessage{
				nodeID:   node.GetNodeID(),
				msgType:  msg.Type,
				sequence: msg.Sequence,
			}
		})
	}

	// Start network transport for each node
	fmt.Println("Starting network transport...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, node := range nodes {
		if err := node.Start(ctx); err != nil {
			t.Fatalf("Failed to start node: %v", err)
		}
	}

	// Wait for network connections
	fmt.Println("Waiting for network connections...")
	time.Sleep(2 * time.Second)

	// Test prompt
	prompt := "What is the capital of France? Answer in one sentence."
	requestID := "test-consensus-1"

	fmt.Printf("\nSubmitting prompt to leader node: '%s'\n", prompt)

	// Define leader node
	leaderNode := nodes[0]

	// Message verification maps
	prePrepareReceived := make(map[string]bool)
	prepareMessages := make(map[string]map[string]bool)        // sequence -> nodeID -> sent
	commitMessages := make(map[string]map[string]bool)         // sequence -> nodeID -> sent
	messagesByNode := make(map[string][]consensus.MessageType) // nodeID -> message types received

	// Start message verification goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range msgChan {
			// Track all messages for each node
			messagesByNode[msg.nodeID] = append(messagesByNode[msg.nodeID], msg.msgType)

			switch msg.msgType {
			case consensus.PrePrepare:
				// Record when any non-leader node receives a PrePrepare
				receiverNode := msg.nodeID
				if receiverNode != leaderNode.GetNodeID() {
					prePrepareReceived[receiverNode] = true
					fmt.Printf("Node %s received PrePrepare message (seq: %d)\n", receiverNode, msg.sequence)
				}
			case consensus.Prepare:
				seqKey := fmt.Sprint(msg.sequence)
				if prepareMessages[seqKey] == nil {
					prepareMessages[seqKey] = make(map[string]bool)
				}
				prepareMessages[seqKey][msg.nodeID] = true
				fmt.Printf("Node %s sent Prepare message (seq: %d)\n", msg.nodeID, msg.sequence)
			case consensus.Commit:
				seqKey := fmt.Sprint(msg.sequence)
				if commitMessages[seqKey] == nil {
					commitMessages[seqKey] = make(map[string]bool)
				}
				commitMessages[seqKey][msg.nodeID] = true
				fmt.Printf("Node %s sent Commit message (seq: %d)\n", msg.nodeID, msg.sequence)
			}
		}
	}()

	// Submit prompt to leader node only
	err := leaderNode.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process prompt on leader node: %v", err)
	}

	fmt.Println("\nWaiting for consensus...")
	time.Sleep(10 * time.Second)

	// Verify PBFT message flow
	fmt.Println("\n=== PBFT Message Flow ===")

	// Verify PrePrepare
	fmt.Println("\nPrePrepare phase:")
	nonLeaderNodes := len(nodes) - 1
	receivedCount := 0
	for nodeID, received := range prePrepareReceived {
		if nodeID != leaderNode.GetNodeID() && received {
			receivedCount++
			fmt.Printf("✓ Node %s received PrePrepare\n", nodeID)
		}
	}
	if receivedCount < nonLeaderNodes {
		t.Errorf("Not all non-leader nodes received PrePrepare message (got %d, need %d)",
			receivedCount, nonLeaderNodes)
	}

	// Verify Prepare
	fmt.Println("\nPrepare phase:")
	for seq, nodes := range prepareMessages {
		fmt.Printf("Sequence %s:\n", seq)
		for nodeID := range nodes {
			fmt.Printf("✓ Node %s sent Prepare\n", nodeID)
		}
		if len(nodes) < 2*(len(nodeIDs)-1)/3 {
			t.Errorf("Insufficient Prepare messages for sequence %s (got %d, need %d)",
				seq, len(nodes), 2*(len(nodeIDs)-1)/3)
		}
	}

	// Verify Commit
	fmt.Println("\nCommit phase:")
	for seq, nodes := range commitMessages {
		fmt.Printf("Sequence %s:\n", seq)
		for nodeID := range nodes {
			fmt.Printf("✓ Node %s sent Commit\n", nodeID)
		}
		if len(nodes) < 2*(len(nodeIDs)-1)/3+1 {
			t.Errorf("Insufficient Commit messages for sequence %s (got %d, need %d)",
				seq, len(nodes), 2*(len(nodeIDs)-1)/3+1)
		}
	}

	// Print message summary for each node
	fmt.Println("\n=== Message Summary ===")
	for nodeID, messages := range messagesByNode {
		fmt.Printf("\nNode %s received:\n", nodeID)
		msgCounts := make(map[consensus.MessageType]int)
		for _, msgType := range messages {
			msgCounts[msgType]++
		}
		for msgType, count := range msgCounts {
			var typeStr string
			switch msgType {
			case consensus.PrePrepare:
				typeStr = "PrePrepare"
			case consensus.Prepare:
				typeStr = "Prepare"
			case consensus.Commit:
				typeStr = "Commit"
			case consensus.ViewChange:
				typeStr = "ViewChange"
			case consensus.NewView:
				typeStr = "NewView"
			case consensus.StateTransfer:
				typeStr = "StateTransfer"
			default:
				typeStr = "Unknown"
			}
			fmt.Printf("- %s messages: %d\n", typeStr, count)
		}
	}

	// Check results from each node
	fmt.Println("\n=== Node Responses ===")
	for i, node := range nodes {
		state, err := node.GetRequestState(requestID)
		if err != nil {
			t.Fatalf("Failed to get request state from node %s: %v", nodeIDs[i], err)
		}

		fmt.Printf("\nNode: %s\n", nodeIDs[i])
		fmt.Printf("Status: %s\n", state.Status)

		if state.LLMResponse != nil {
			fmt.Printf("LLM Response: %s\n", state.LLMResponse.Text)
		}

		if state.Clusters != nil && len(state.Clusters) > 0 {
			fmt.Println("\nSemantic Clusters:")
			for i, cluster := range state.Clusters {
				fmt.Printf("\nCluster %d (Average Score: %.3f):\n", i+1, cluster.AverageScore)
				for _, resp := range cluster.Responses {
					fmt.Printf("- [%s] %s\n", resp.NodeID, resp.Text)
				}
			}
		}

		if state.ConsensusData != nil {
			fmt.Println("\nConsensus Data:")
			data, _ := json.MarshalIndent(state.ConsensusData, "", "  ")
			fmt.Println(string(data))
		}

		if state.IPFSCID != "" {
			fmt.Printf("\nIPFS CID: %s\n", state.IPFSCID)
		}
	}

	// Verify consensus was reached
	var finalResponse string
	var consensusReached bool

	for _, node := range nodes {
		state, _ := node.GetRequestState(requestID)
		if state.Status == "complete" && state.ConsensusData != nil && state.ConsensusData.Response != nil {
			if finalResponse == "" {
				finalResponse = state.ConsensusData.Response.Text
				consensusReached = true
			} else if state.ConsensusData.Response.Text != finalResponse {
				consensusReached = false
				break
			}
		}
	}

	fmt.Println("\n=== Test Results ===")
	if consensusReached {
		fmt.Println("✅ Consensus reached successfully!")
		fmt.Printf("Final response: %s\n", finalResponse)
	} else {
		t.Error("❌ Consensus failed - nodes have different final values")
	}

	// Clean up
	close(msgChan)
	<-done
	for _, node := range nodes {
		if err := node.Stop(); err != nil {
			t.Logf("Warning: failed to stop node: %v", err)
		}
	}
}
