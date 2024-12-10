package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mhollas/7610/agent/llm"
	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/logging"
	"github.com/mhollas/7610/oracle/pkg/node"
	"github.com/mhollas/7610/oracle/pkg/similarity"
	"github.com/mhollas/7610/oracle/pkg/storage"
)

var (
	nodes          []*node.OracleNode
	requestCounter = 0
	appConfig      *Config
)

type Config struct {
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

type PromptRequest struct {
	Prompt string `json:"prompt"`
}

type pbftMessage struct {
	nodeID   string
	msgType  consensus.MessageType
	sequence uint64
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Absolute path to config file (required)")
	flag.Parse()

	// Validate config path
	if *configPath == "" {
		log.Fatal("Config path is required. Use -config flag to specify the absolute path to config file.")
	}

	// // Check if path is absolute
	// if !filepath.IsAbs(*configPath) {
	// 	log.Fatal("Config path must be absolute. Current path is relative:", *configPath)
	// }

	// Read config file from specified path
	configFile, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to read config file from %s: %v", *configPath, err)
	}

	appConfig = &Config{}
	if err := json.Unmarshal(configFile, appConfig); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	// Create test nodes - same as test
	nodeIDs := []string{"leader", "peer1", "peer2", "peer3", "peer4", "peer5", "peer6"}
	nodes = make([]*node.OracleNode, len(nodeIDs))
	addresses := make(map[string]string)

	fmt.Println("\n=== Starting Oracle Network ===")
	fmt.Printf("Creating leader node with %d peers (f = %d, total = 3f + 1)...\n", len(nodeIDs)-1, (len(nodeIDs)-1)/3)

	// Create nodes with same configuration as test
	for i, nodeID := range nodeIDs {
		var oracleNode *node.OracleNode
		addr := fmt.Sprintf("localhost:%d", 9000+i)
		addresses[nodeID] = addr

		// Create LLM client
		llmConfig := &llm.Config{
			Model:              appConfig.LLM.OpenAI.Model,
			MaxTokens:          appConfig.LLM.OpenAI.MaxTokens,
			DefaultTemperature: appConfig.LLM.OpenAI.Temperature,
			MaxRetries:         appConfig.LLM.OpenAI.MaxRetries,
			RetryIntervalMs:    appConfig.LLM.OpenAI.RetryIntervalMS,
		}
		llmClient, err := llm.NewOpenAIClient(appConfig.LLM.OpenAI.APIKey, llmConfig)
		if err != nil {
			log.Fatalf("Failed to create LLM client: %v", err)
		}

		// Create storage client (only for leader)
		var storageClient storage.StorageClient
		var scorer *similarity.SemanticScorer
		if i == 0 {
			var err error
			storageClient, err = storage.NewPinataClient(
				appConfig.Oracle.Storage.Pinata.APIKey,
				appConfig.Oracle.Storage.Pinata.APISecret,
				appConfig.Oracle.Storage.Pinata.JWT,
			)
			if err != nil {
				log.Fatalf("Failed to create storage client: %v", err)
			}
			scorer = similarity.NewSemanticScorer(appConfig.LLM.OpenAI.APIKey, "")
		}

		// Create logger
		logger := logging.NewLogger(logging.DEBUG, nil, nodeID)

		// Create node with all capabilities
		oracleNode, err = node.NewOracleNode(nodeID, addr, llmClient, storageClient, scorer, logger)
		if err != nil {
			log.Fatalf("Failed to create node %s: %v", nodeID, err)
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
					log.Fatalf("Failed to register peer %s: %v", peerID, err)
				}
			}
		}
	}

	// Start network transport for each node
	fmt.Println("Starting network transport...")
	ctx := context.Background()

	for _, node := range nodes {
		if err := node.Start(ctx); err != nil {
			log.Fatalf("Failed to start node: %v", err)
		}
	}

	// Wait for network connections
	fmt.Println("Waiting for network connections...")
	time.Sleep(2 * time.Second)

	// Setup HTTP handlers
	http.HandleFunc("/prompt", handlePrompt)

	fmt.Println("\n=== Oracle Network Ready ===")
	fmt.Println("Listening for prompts on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func handlePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PromptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	requestCounter++
	requestID := fmt.Sprintf("request-%d", requestCounter)

	// Message verification maps
	prePrepareReceived := make(map[string]bool)
	prepareMessages := make(map[string]map[string]bool)        // sequence -> nodeID -> sent
	commitMessages := make(map[string]map[string]bool)         // sequence -> nodeID -> sent
	messagesByNode := make(map[string][]consensus.MessageType) // nodeID -> message types received

	// Create message verification channel
	msgChan := make(chan pbftMessage, 1000)
	done := make(chan struct{})

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

	// Start message verification goroutine
	go func() {
		defer close(done)
		for {
			select {
			case msg, ok := <-msgChan:
				if !ok {
					return
				}
				// Track all messages for each node
				messagesByNode[msg.nodeID] = append(messagesByNode[msg.nodeID], msg.msgType)

				switch msg.msgType {
				case consensus.PrePrepare:
					// Record when any non-leader node receives a PrePrepare
					receiverNode := msg.nodeID
					if receiverNode != nodes[0].GetNodeID() {
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
			case <-time.After(12 * time.Second): // Timeout after waiting for messages
				return
			}
		}
	}()

	// Submit prompt to all nodes and collect initial responses
	fmt.Printf("\nSubmitting prompt to all nodes: '%s'\n", requestID, req.Prompt)
	initialResponses := make(map[string]string)
	for _, node := range nodes {
		if err := node.ProcessPrompt(context.Background(), requestID, req.Prompt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to process prompt: %v", err), http.StatusInternalServerError)
			return
		}
		fmt.Printf("Submitted prompt to node %s\n", node.GetNodeID())

		// Wait a bit for LLM response
		time.Sleep(2 * time.Second)

		// Get initial response
		state, err := node.GetRequestState(requestID)
		if err != nil {
			continue
		}
		if state.LLMResponse != nil {
			initialResponses[node.GetNodeID()] = state.LLMResponse.Text
			fmt.Printf("\nInitial response from %s: %s\n", node.GetNodeID(), state.LLMResponse.Text)
		}
	}

	fmt.Println("\nWaiting for consensus...")
	time.Sleep(10 * time.Second)

	// Wait for message collection to complete
	<-done

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
		if nodeID != nodes[0].GetNodeID() && received {
			receivedCount++
			fmt.Printf("✓ Node %s received PrePrepare\n", nodeID)
		}
	}
	if receivedCount < nonLeaderNodes {
		fmt.Printf("Not all non-leader nodes received PrePrepare message (got %d, need %d)\n",
			receivedCount, nonLeaderNodes)
	}

	// Verify Prepare
	fmt.Println("\nPrepare phase:")
	for seq, nodes := range prepareMessages {
		fmt.Printf("Sequence %s:\n", seq)
		for nodeID := range nodes {
			fmt.Printf("✓ Node %s sent Prepare\n", nodeID)
		}
	}

	// Verify Commit
	fmt.Println("\nCommit phase:")
	for seq, nodes := range commitMessages {
		fmt.Printf("Sequence %s:\n", seq)
		for nodeID := range nodes {
			fmt.Printf("✓ Node %s sent Commit\n", nodeID)
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
	var finalResponse string
	var consensusReached bool

	for _, node := range nodes {
		state, err := node.GetRequestState(requestID)
		if err != nil {
			continue
		}

		fmt.Printf("\nNode: %s\n", node.GetNodeID())
		fmt.Printf("Status: %s\n", state.Status)

		// Store response from LLM response only (not consensus data)
		if state.LLMResponse != nil {
			fmt.Printf("LLM Response: %s\n", state.LLMResponse.Text)
			allResponses[node.GetNodeID()] = state.LLMResponse.Text
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

		// Check consensus
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

	// Calculate and print semantic similarity matrix
	fmt.Println("\n=== Semantic Similarity Matrix ===")
	var similarityMatrix map[string]map[string]float32

	if len(allResponses) > 0 {
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

		// Use the scorer from the leader node's configuration
		scorer := similarity.NewSemanticScorer(appConfig.LLM.OpenAI.APIKey, "")
		if scorer != nil {
			// Get embeddings for all responses
			embeddings := make(map[string][]float32)
			for nodeID, resp := range allResponses {
				embedding, err := scorer.GetEmbedding(context.Background(), resp)
				if err != nil {
					fmt.Printf("Failed to get embedding for node %s: %v\n", nodeID, err)
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
		}
	}

	fmt.Println("\n=== Final Results ===")
	if consensusReached {
		fmt.Println("✅ Consensus reached successfully!")
		fmt.Printf("Final response: %s\n", finalResponse)
	} else {
		fmt.Println("❌ Consensus not reached")
	}

	fmt.Println("\n=== Request Complete ===")
	fmt.Printf("Request ID: %s\n", requestID)
	fmt.Println("Sending response to client...")

	// Get IPFS CID from the leader node (node 0)
	var ipfsCID string
	leaderState, err := nodes[0].GetRequestState(requestID)
	if err == nil && leaderState.IPFSCID != "" {
		ipfsCID = leaderState.IPFSCID
	}

	// Prepare simplified response with just IPFS CID
	response := map[string]interface{}{
		"ipfs_cid": ipfsCID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
