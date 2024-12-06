package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mhollas/7610/agent/llm"
	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/logging"
	"github.com/mhollas/7610/oracle/pkg/node"
	"github.com/mhollas/7610/oracle/pkg/similarity"
	"github.com/mhollas/7610/oracle/pkg/storage"
)

func TestOracleConsensus(t *testing.T) {
	// Read config file
	configFile, err := os.ReadFile("../../../config/config.json")
	if err != nil {
		t.Fatalf("Failed to read config file: %v", err)
	}

	var config struct {
		Oracle struct {
			Storage struct {
				Provider string `json:"provider"`
				Pinata   struct {
					APIKey    string `json:"api_key"`
					APISecret string `json:"api_secret"`
					JWT       string `json:"jwt"`
				} `json:"pinata"`
			} `json:"storage"`
		} `json:"oracle"`
		LLM struct {
			OpenAI struct {
				APIKey          string  `json:"api_key"`
				Model           string  `json:"model"`
				Temperature     float32 `json:"temperature"`
				MaxTokens       int     `json:"max_tokens"`
				MaxRetries      int     `json:"max_retries"`
				RetryIntervalMS int     `json:"retry_interval_ms"`
			} `json:"openai"`
		} `json:"llm"`
	}

	if err := json.Unmarshal(configFile, &config); err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	// Create test nodes
	nodeIDs := []string{"leader", "peer1", "peer2", "peer3", "peer4", "peer5", "peer6"}
	nodes := make([]*node.OracleNode, len(nodeIDs))
	addresses := make(map[string]string)

	fmt.Println("\n=== Starting Oracle Consensus Test ===")
	fmt.Printf("Creating leader node with %d peers (f = %d, total = 3f + 1)...\n", len(nodeIDs)-1, (len(nodeIDs)-1)/3)

	// Create shared components for leader
	cfg := &llm.Config{
		Model:              config.LLM.OpenAI.Model,
		MaxTokens:          config.LLM.OpenAI.MaxTokens,
		DefaultTemperature: config.LLM.OpenAI.Temperature,
		MaxRetries:         config.LLM.OpenAI.MaxRetries,
		RetryIntervalMs:    config.LLM.OpenAI.RetryIntervalMS,
	}

	// Initialize nodes
	for i, nodeID := range nodeIDs {
		var oracleNode *node.OracleNode
		var err error

		// Create node address
		addr := fmt.Sprintf("localhost:%d", 8545+i)
		addresses[nodeID] = addr

		// Create LLM client for all nodes
		llmClient, err := llm.NewOpenAIClient(config.LLM.OpenAI.APIKey, cfg)
		if err != nil {
			t.Fatalf("Failed to create LLM client for node %s: %v", nodeID, err)
		}

		// Create real Pinata storage client
		storageClient, err := storage.NewPinataClient(
			config.Oracle.Storage.Pinata.APIKey,
			config.Oracle.Storage.Pinata.APISecret,
			config.Oracle.Storage.Pinata.JWT,
		)
		if err != nil {
			t.Fatalf("Failed to create storage client for node %s: %v", nodeID, err)
		}

		// Create semantic scorer
		scorer := similarity.NewSemanticScorer(config.LLM.OpenAI.APIKey, "")

		// Create logger
		logger := logging.NewLogger(logging.DEBUG, nil, nodeID)

		// Create node with all capabilities
		oracleNode, err = node.NewOracleNode(nodeID, addr, llmClient, storageClient, scorer, logger)
		if err != nil {
			t.Fatalf("Failed to create node %s: %v", nodeID, err)
		}
		fmt.Printf("Created node %s at %s with storage capabilities\n", nodeID, addr)

		// Initialize consensus for all nodes
		oracleNode.InitConsensus(nodeIDs)
		nodes[i] = oracleNode
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
	prompt := "Tell me a short poem about Moon. Be creative."
	requestID := "test-consensus-1"

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

	// Submit prompt to all nodes and collect initial responses
	fmt.Printf("\nSubmitting prompt to all nodes: '%s'\n", prompt)
	initialResponses := make(map[string]string)
	for _, node := range nodes {
		if err := node.ProcessPrompt(ctx, requestID, prompt); err != nil {
			t.Fatalf("Failed to process prompt on node %s: %v", node.GetNodeID(), err)
		}
		fmt.Printf("Submitted prompt to node %s\n", node.GetNodeID())

		// Wait a bit for LLM response
		time.Sleep(2 * time.Second)

		// Get initial response
		state, err := node.GetRequestState(requestID)
		if err != nil {
			t.Fatalf("Failed to get initial state from node %s: %v", node.GetNodeID(), err)
		}
		if state.LLMResponse != nil {
			initialResponses[node.GetNodeID()] = state.LLMResponse.Text
			fmt.Printf("\nInitial response from %s: %s\n", node.GetNodeID(), state.LLMResponse.Text)
		}
	}

	fmt.Println("\nWaiting for consensus...")
	time.Sleep(10 * time.Second)

	// Print initial responses
	fmt.Println("\n=== Initial Node Responses (Before Consensus) ===")
	for nodeID, response := range initialResponses {
		fmt.Printf("\nNode %s initial response:\n%s\n", nodeID, response)
	}

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
	allResponses := make(map[string]string)

	for i, node := range nodes {
		state, err := node.GetRequestState(requestID)
		if err != nil {
			t.Fatalf("Failed to get request state from node %s: %v", nodeIDs[i], err)
		}

		fmt.Printf("\nNode: %s\n", nodeIDs[i])
		fmt.Printf("Status: %s\n", state.Status)

		// Store response from LLM response only (not consensus data)
		if state.LLMResponse != nil {
			fmt.Printf("LLM Response: %s\n", state.LLMResponse.Text)
			allResponses[nodeIDs[i]] = state.LLMResponse.Text
		}

		// Print consensus data separately
		if state.ConsensusData != nil {
			fmt.Println("\nConsensus Data:")
			data, _ := json.MarshalIndent(state.ConsensusData, "", "  ")
			fmt.Println(string(data))
		}

		if state.IPFSCID != "" {
			fmt.Printf("\nIPFS CID: %s\n", state.IPFSCID)
		}
	}

	// Print semantic similarity matrix only if we have unique responses
	var similarityMatrix map[string]map[string]float32
	var messageStats map[string]map[consensus.MessageType]int

	if len(allResponses) > 0 {
		fmt.Println("\n=== Individual Node Responses ===")
		for nodeID, response := range allResponses {
			fmt.Printf("\nNode %s LLM response:\n%s\n", nodeID, response)
		}

		fmt.Println("\n=== Semantic Similarity Matrix ===")
		// Print header row
		fmt.Printf("%-10s", "Node")
		for nodeID := range allResponses {
			displayID := nodeID
			if len(nodeID) > 10 {
				displayID = nodeID[:10]
			}
			fmt.Printf("| %-8s ", displayID)
		}
		fmt.Println("|")

		// Print separator
		fmt.Printf("%-10s", "----------")
		for range allResponses {
			fmt.Printf("|%s", "---------")
		}
		fmt.Println("|")

		// Create scorer for similarity calculation
		scorer := similarity.NewSemanticScorer(config.LLM.OpenAI.APIKey, "")

		// Get embeddings for all responses
		embeddings := make(map[string][]float32)
		for nodeID, resp := range allResponses {
			embedding, err := scorer.GetEmbedding(context.Background(), resp)
			if err != nil {
				t.Logf("Failed to get embedding for node %s: %v", nodeID, err)
				continue
			}
			embeddings[nodeID] = embedding
			fmt.Printf("Got embedding for node %s, length: %d\n", nodeID, len(embedding))
		}

		// Print similarity scores
		similarityMatrix = make(map[string]map[string]float32)
		for nodeID1 := range allResponses {
			similarityMatrix[nodeID1] = make(map[string]float32)
			displayID := nodeID1
			if len(nodeID1) > 10 {
				displayID = nodeID1[:10]
			}
			fmt.Printf("%-10s", displayID)
			for nodeID2 := range allResponses {
				if nodeID1 == nodeID2 {
					fmt.Printf("| %-8s ", "1.000")
					similarityMatrix[nodeID1][nodeID2] = 1.0
					continue
				}

				// Get embeddings
				emb1, ok1 := embeddings[nodeID1]
				emb2, ok2 := embeddings[nodeID2]
				if !ok1 || !ok2 {
					fmt.Printf("| %-8s ", "error")
					continue
				}

				// Calculate similarity
				score := similarity.CosineSimilarity(emb1, emb2)
				similarityMatrix[nodeID1][nodeID2] = score
				fmt.Printf("| %-8.3f ", score)
			}
			fmt.Println("|")
		}

		// Collect message statistics
		messageStats = make(map[string]map[consensus.MessageType]int)
		for nodeID, messages := range messagesByNode {
			if messageStats[nodeID] == nil {
				messageStats[nodeID] = make(map[consensus.MessageType]int)
			}
			for _, msgType := range messages {
				messageStats[nodeID][msgType]++
			}
		}

		// Print message statistics
		fmt.Println("\n=== Message Statistics ===")
		for nodeID, stats := range messageStats {
			fmt.Printf("\nNode %s:\n", nodeID)
			fmt.Printf("PrePrepare messages: %d\n", stats[consensus.PrePrepare])
			fmt.Printf("Prepare messages: %d\n", stats[consensus.Prepare])
			fmt.Printf("Commit messages: %d\n", stats[consensus.Commit])
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

		// Store final results in IPFS
		finalResults := &storage.ExecutionRecord{
			RequestID:    requestID,
			RequestInput: prompt,
			Timestamp:    time.Now(),
			OracleResponses: func() []storage.OracleResponse {
				responses := make([]storage.OracleResponse, 0, len(allResponses))
				for nodeID, text := range allResponses {
					responses = append(responses, storage.OracleResponse{
						NodeID:      nodeID,
						LLMResponse: text,
						Timestamp:   time.Now(),
						Metadata: map[string]interface{}{
							"messageStats": messageStats[nodeID],
						},
					})
				}
				return responses
			}(),
			Consensus: storage.ConsensusData{
				Method:           "PBFT",
				ParticipantCount: len(nodes),
				AgreementScore:   1.0,
				Round:            1,
			},
			FinalResponse: finalResponse,
			ExtraMetadata: map[string]interface{}{
				"messageStats":     messageStats,
				"similarityMatrix": similarityMatrix,
			},
		}

		// Store using the first node's storage client
		if storageClient, ok := nodes[0].GetStorage().(storage.StorageClient); ok {
			cid, err := storageClient.StoreExecutionRecord(context.Background(), finalResults)
			if err != nil {
				t.Logf("Warning: Failed to store final test results: %v", err)
			} else {
				fmt.Printf("\n=== Final Results Stored ===\nIPFS CID: %s\n", cid)
				fmt.Println("\nStored data includes:")
				fmt.Println("- Individual node responses")
				fmt.Println("- Semantic similarity matrix")
				fmt.Println("- Message statistics per node")
				fmt.Println("- Final consensus response")
			}
		}
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
