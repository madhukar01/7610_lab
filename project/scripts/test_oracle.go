package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"oracle/internal/storage"
)

func main() {
	// Read config
	configFile, err := os.ReadFile("config/config.json")
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

	// Create storage client
	storageClient, err := storage.NewPinataClient(
		config.Oracle.Storage.Pinata.APIKey,
		config.Oracle.Storage.Pinata.APISecret,
		config.Oracle.Storage.Pinata.JWT,
	)
	if err != nil {
		log.Fatalf("Failed to create storage client: %v", err)
	}

	// Create test record
	record := &storage.ExecutionRecord{
		RequestID:    "test-request-1",
		RequestInput: "What is the current weather in New York?",
		Timestamp:    time.Now(),
		OracleResponses: []storage.OracleResponse{
			{
				NodeID:      "node1",
				LLMResponse: "The current weather in New York is sunny with a temperature of 75°F",
				Timestamp:   time.Now(),
				Metadata: map[string]interface{}{
					"model":       "gpt-4",
					"temperature": 0.7,
				},
			},
			{
				NodeID:      "node2",
				LLMResponse: "New York is experiencing sunny conditions with 75 degrees Fahrenheit",
				Timestamp:   time.Now(),
				Metadata: map[string]interface{}{
					"model":       "gpt-4",
					"temperature": 0.7,
				},
			},
		},
		Consensus: storage.ConsensusData{
			Method:           "Semantic-PBFT",
			ParticipantCount: 2,
			AgreementScore:   0.95,
			Round:            1,
		},
		FinalResponse: "The current weather in New York is sunny with a temperature of 75°F",
	}

	// Test storing record
	ctx := context.Background()
	fmt.Println("Storing test record...")
	cid, err := storageClient.StoreExecutionRecord(ctx, record)
	if err != nil {
		log.Fatalf("Failed to store record: %v", err)
	}
	fmt.Printf("Successfully stored record with CID: %s\n", cid)

	// Test retrieving record
	fmt.Println("\nRetrieving test record...")
	retrieved, err := storageClient.GetExecutionRecord(ctx, cid)
	if err != nil {
		log.Fatalf("Failed to retrieve record: %v", err)
	}

	// Verify retrieved data
	fmt.Printf("Retrieved record:\n")
	fmt.Printf("  Request ID: %s\n", retrieved.RequestID)
	fmt.Printf("  Input: %s\n", retrieved.RequestInput)
	fmt.Printf("  Final Response: %s\n", retrieved.FinalResponse)
	fmt.Printf("  Number of Oracle Responses: %d\n", len(retrieved.OracleResponses))
	fmt.Printf("  Consensus Score: %.2f\n", retrieved.Consensus.AgreementScore)
}
