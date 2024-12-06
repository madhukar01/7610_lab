package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agent/src/llm"
	"oracle/internal/similarity"
	"oracle/internal/storage"
)

type NodeResponse struct {
	NodeID    string
	Response  *llm.LLMResponse
	Error     error
	Timestamp time.Time
}

// cosineSimilarity calculates the cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct float32
	var normA float32
	var normB float32

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

func main() {
	// Parse command line flags
	numNodes := flag.Int("nodes", 3, "Number of nodes to simulate")
	useIPFS := flag.Bool("ipfs", false, "Upload responses to IPFS (default: store locally)")
	flag.Parse()

	// Get the absolute path to the workspace root
	workspaceRoot := filepath.Join("..", "..", "..")

	// Read config
	configFile, err := os.ReadFile(filepath.Join(workspaceRoot, "config", "config.json"))
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
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
		log.Fatalf("Failed to parse config: %v", err)
	}

	fmt.Printf("Using OpenAI Model: %s\n", config.LLM.OpenAI.Model)
	fmt.Printf("Temperature: %.2f\n", config.LLM.OpenAI.Temperature)
	fmt.Printf("Number of nodes: %d\n", *numNodes)
	if *useIPFS {
		fmt.Println("Storage: IPFS via Pinata")
	} else {
		fmt.Println("Storage: Local files")
	}

	// Create semantic scorer for consensus
	scorer := similarity.NewSemanticScorer(config.LLM.OpenAI.APIKey, "")

	// Create storage client if using IPFS
	var storageClient storage.StorageClient
	if *useIPFS {
		storageClient, err = storage.NewPinataClient(
			config.Oracle.Storage.Pinata.APIKey,
			config.Oracle.Storage.Pinata.APISecret,
			config.Oracle.Storage.Pinata.JWT,
		)
		if err != nil {
			log.Fatalf("Failed to create storage client: %v", err)
		}
	}

	// Create responses directory
	responsesDir := filepath.Join("responses")
	if err := os.MkdirAll(responsesDir, 0755); err != nil {
		log.Fatalf("Failed to create responses directory: %v", err)
	}

	// Test prompts
	prompts := []struct {
		id   string
		text string
	}{
		{
			id:   "test-1",
			text: "What is the capital of France?",
		},
		{
			id:   "test-2",
			text: "Write a short poem about the moon.",
		},
	}

	ctx := context.Background()

	// Process each prompt
	for _, prompt := range prompts {
		fmt.Printf("\nProcessing prompt: %s\n", prompt.text)

		// Create channels for node responses
		responsesChan := make(chan NodeResponse, *numNodes)
		var wg sync.WaitGroup

		// Launch nodes
		for i := 0; i < *numNodes; i++ {
			wg.Add(1)
			go func(nodeID string) {
				defer wg.Done()

				// Create OpenAI client for this node
				llmConfig := &llm.Config{
					Model:              config.LLM.OpenAI.Model,
					MaxTokens:          config.LLM.OpenAI.MaxTokens,
					DefaultTemperature: config.LLM.OpenAI.Temperature,
					MaxRetries:         config.LLM.OpenAI.MaxRetries,
					RetryIntervalMs:    int(config.LLM.OpenAI.RetryIntervalMS),
				}
				llmClient, err := llm.NewOpenAIClient(config.LLM.OpenAI.APIKey, llmConfig)
				if err != nil {
					responsesChan <- NodeResponse{NodeID: nodeID, Error: err}
					return
				}

				// Get LLM response
				llmResp, err := llmClient.GetResponse(ctx, &llm.LLMRequest{
					Prompt:      prompt.text,
					Temperature: config.LLM.OpenAI.Temperature,
					RequestID:   prompt.id,
					NodeID:      nodeID,
					ExtraParams: map[string]interface{}{
						"timestamp": time.Now(),
					},
				})

				responsesChan <- NodeResponse{
					NodeID:    nodeID,
					Response:  llmResp,
					Error:     err,
					Timestamp: time.Now(),
				}
			}(fmt.Sprintf("node-%d", i+1))
		}

		// Wait for all nodes and collect responses
		go func() {
			wg.Wait()
			close(responsesChan)
		}()

		// Collect responses
		var responses []NodeResponse
		for resp := range responsesChan {
			if resp.Error != nil {
				log.Printf("Node %s error: %v", resp.NodeID, resp.Error)
				continue
			}
			responses = append(responses, resp)
		}

		if len(responses) == 0 {
			log.Printf("No successful responses for prompt: %s", prompt.text)
			continue
		}

		// Convert responses for semantic scoring
		semanticResponses := make([]*similarity.Response, 0, len(responses))
		for _, resp := range responses {
			semanticResponses = append(semanticResponses, &similarity.Response{
				Text:   resp.Response.Text,
				NodeID: resp.NodeID,
			})
		}

		// Get semantic clusters
		clusters, err := scorer.ClusterResponses(ctx, semanticResponses, 0.85)
		if err != nil {
			log.Printf("Failed to cluster responses: %v", err)
			continue
		}

		// Select consensus response
		consensusResp := scorer.SelectConsensusResponse(clusters)
		if consensusResp == nil {
			log.Printf("Failed to select consensus response")
			continue
		}

		// Create execution record
		record := &storage.ExecutionRecord{
			RequestID:       prompt.id,
			RequestInput:    prompt.text,
			Timestamp:       time.Now(),
			OracleResponses: make([]storage.OracleResponse, 0, len(responses)),
			Consensus: storage.ConsensusData{
				Method:           "Semantic-Clustering",
				ParticipantCount: len(responses),
				AgreementScore:   clusters[0].AverageScore,
				Round:            1,
			},
			FinalResponse: consensusResp.Text,
		}

		// Add all responses
		for _, resp := range responses {
			record.OracleResponses = append(record.OracleResponses, storage.OracleResponse{
				NodeID:      resp.NodeID,
				LLMResponse: resp.Response.Text,
				Timestamp:   resp.Timestamp,
				Metadata:    resp.Response.ExtraParams,
			})
		}

		// Store record
		if *useIPFS {
			// Store in IPFS
			fmt.Println("Storing execution record in IPFS...")
			cid, err := storageClient.StoreExecutionRecord(ctx, record)
			if err != nil {
				log.Printf("Failed to store record in IPFS: %v", err)
				continue
			}
			fmt.Printf("Successfully stored record with CID: %s\n", cid)
			fmt.Printf("View on IPFS: https://gateway.pinata.cloud/ipfs/%s\n", cid)
		}

		// Store locally
		filename := fmt.Sprintf("responses/%s_%s.json",
			time.Now().Format("20060102_150405"),
			prompt.id,
		)
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			log.Printf("Failed to marshal record: %v", err)
			continue
		}
		if err := os.WriteFile(filename, data, 0644); err != nil {
			log.Printf("Failed to write record: %v", err)
			continue
		}
		fmt.Printf("Stored response locally: %s\n", filename)

		// Print results
		fmt.Printf("\nConsensus Results:\n")
		fmt.Printf("  Request ID: %s\n", record.RequestID)
		fmt.Printf("  Input: %s\n", record.RequestInput)
		fmt.Printf("  Number of Responses: %d\n", len(responses))
		fmt.Printf("  Agreement Score: %.2f\n", record.Consensus.AgreementScore)
		fmt.Printf("  Consensus Response: %s\n", record.FinalResponse)

		// Print timing metrics
		var totalTokens int
		var minLatency, maxLatency time.Duration
		var totalLatency time.Duration
		responseStartTimes := make(map[string]time.Time)
		responseEndTimes := make(map[string]time.Time)

		for _, resp := range responses {
			if tokens, ok := resp.Response.ExtraParams["completion_tokens"].(int); ok {
				totalTokens += tokens
			}
			if startTime, ok := resp.Response.ExtraParams["timestamp"].(time.Time); ok {
				responseStartTimes[resp.NodeID] = startTime
				responseEndTimes[resp.NodeID] = resp.Timestamp
				latency := resp.Timestamp.Sub(startTime)
				totalLatency += latency
				if minLatency == 0 || latency < minLatency {
					minLatency = latency
				}
				if latency > maxLatency {
					maxLatency = latency
				}
			}
		}

		fmt.Printf("\nPerformance Metrics:\n")
		fmt.Printf("  Total Tokens Used: %d\n", totalTokens)
		fmt.Printf("  Average Tokens per Response: %.1f\n", float64(totalTokens)/float64(len(responses)))
		fmt.Printf("  Min Latency: %v\n", minLatency)
		fmt.Printf("  Max Latency: %v\n", maxLatency)
		fmt.Printf("  Average Latency: %v\n", totalLatency/time.Duration(len(responses)))

		// Print cluster analysis
		fmt.Printf("\nCluster Analysis:\n")
		for i, cluster := range clusters {
			fmt.Printf("  Cluster %d:\n", i+1)
			fmt.Printf("    Size: %d responses\n", len(cluster.Responses))
			fmt.Printf("    Agreement Score: %.2f\n", cluster.AverageScore)
			fmt.Printf("    Representative: %s\n", cluster.Representative.Text)
			fmt.Printf("    Variations:\n")
			for _, resp := range cluster.Responses {
				if resp != cluster.Representative {
					fmt.Printf("      - %s\n", resp.Text)
				}
			}
		}

		// Print response details
		fmt.Printf("\nDetailed Node Responses:\n")
		for _, resp := range responses {
			fmt.Printf("  Node: %s\n", resp.NodeID)
			fmt.Printf("    Response: %s\n", resp.Response.Text)
			fmt.Printf("    Tokens: %d\n", resp.Response.TokensUsed)
			fmt.Printf("    Latency: %v\n", responseEndTimes[resp.NodeID].Sub(responseStartTimes[resp.NodeID]))
			fmt.Printf("    Model: %s\n", resp.Response.Model)
			if temp, ok := resp.Response.ExtraParams["temperature"].(float32); ok {
				fmt.Printf("    Temperature: %.2f\n", temp)
			}
		}

		// Print semantic similarity matrix
		fmt.Printf("\nSemantic Similarity Matrix:\n")
		fmt.Printf("  %10s", "")
		for i := range responses {
			fmt.Printf(" %7s", fmt.Sprintf("Node%d", i+1))
		}
		fmt.Printf("\n")

		for i, r1 := range responses {
			fmt.Printf("  %-10s", fmt.Sprintf("Node%d", i+1))
			for j, r2 := range responses {
				if i == j {
					fmt.Printf(" %7s", "1.00")
				} else {
					// Get embeddings for similarity calculation
					embedding1, err := scorer.GetEmbedding(ctx, r1.Response.Text)
					if err != nil {
						fmt.Printf(" %7s", "err")
						continue
					}
					embedding2, err := scorer.GetEmbedding(ctx, r2.Response.Text)
					if err != nil {
						fmt.Printf(" %7s", "err")
						continue
					}
					similarity := cosineSimilarity(embedding1, embedding2)
					fmt.Printf(" %7.2f", similarity)
				}
			}
			fmt.Printf("\n")
		}
	}
}
