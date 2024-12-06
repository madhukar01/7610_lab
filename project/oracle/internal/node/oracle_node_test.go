package node

import (
	"context"
	"os"
	"testing"
	"time"

	"agent/src/llm"
	"oracle/internal/similarity"
	"oracle/internal/storage"
)

func setupTestNode(t *testing.T) *OracleNode {
	// Get API keys from environment
	apiKey := os.Getenv("OPENAI_API_KEY")
	storageToken := os.Getenv("WEB3_STORAGE_TOKEN")
	if apiKey == "" || storageToken == "" {
		t.Skip("OPENAI_API_KEY or WEB3_STORAGE_TOKEN not set")
	}

	// Create LLM client
	llmConfig := &llm.Config{
		Model:              "gpt-4-1106-preview",
		MaxTokens:          1000,
		DefaultTemperature: 0.7,
		MaxRetries:         3,
		RetryIntervalMs:    1000,
	}
	llmClient, err := llm.NewOpenAIClient(apiKey, llmConfig)
	if err != nil {
		t.Fatalf("Failed to create LLM client: %v", err)
	}

	// Create storage client
	storageClient, err := storage.NewStorageClient(storageToken)
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	// Create semantic scorer
	scorer := similarity.NewSemanticScorer(apiKey, "")

	// Create test nodes
	nodes := []string{"node1", "node2", "node3", "node4"}
	node, err := NewOracleNode("node1", llmClient, storageClient, scorer, nodes)
	if err != nil {
		t.Fatalf("Failed to create oracle node: %v", err)
	}

	return node
}

func TestOracleNodeProcessPrompt(t *testing.T) {
	node := setupTestNode(t)

	// Test processing a prompt
	ctx := context.Background()
	requestID := "test-request-1"
	prompt := "What is the capital of France?"

	err := node.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process prompt: %v", err)
	}

	// Wait for processing
	time.Sleep(5 * time.Second)

	// Check request state
	state, err := node.GetRequestState(requestID)
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	// Verify state
	if state.Status == StatusFailed {
		t.Error("Request processing failed")
	}
	if state.LLMResponse == nil {
		t.Error("No LLM response received")
	}
	if len(state.Responses) == 0 {
		t.Error("No responses collected")
	}
}

func TestOracleNodeDuplicateRequest(t *testing.T) {
	node := setupTestNode(t)

	ctx := context.Background()
	requestID := "test-request-2"
	prompt := "What is 2+2?"

	// First request should succeed
	err := node.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process first prompt: %v", err)
	}

	// Second request with same ID should fail
	err = node.ProcessPrompt(ctx, requestID, prompt)
	if err == nil {
		t.Error("Expected error for duplicate request, got nil")
	}
}

func TestOracleNodeSemanticConsensus(t *testing.T) {
	node := setupTestNode(t)

	// Test with a prompt that might get semantically similar but differently worded responses
	ctx := context.Background()
	requestID := "test-semantic-1"
	prompt := "What is the capital of France?"

	err := node.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process prompt: %v", err)
	}

	// Wait for processing and consensus
	time.Sleep(5 * time.Second)

	// Check request state
	state, err := node.GetRequestState(requestID)
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	// Verify state
	if state.Status == StatusFailed {
		t.Error("Request processing failed")
	}
	if state.Clusters == nil {
		t.Error("No semantic clusters created")
	}
	if len(state.Clusters) == 0 {
		t.Error("Empty semantic clusters")
	}
	if state.Clusters[0].AverageScore == 0 {
		t.Error("No semantic similarity score calculated")
	}
}

func TestOracleNodeDivergentResponses(t *testing.T) {
	node := setupTestNode(t)

	// Test with a prompt that might get very different responses
	ctx := context.Background()
	requestID := "test-semantic-2"
	prompt := "Write a creative story about a cat."

	err := node.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process prompt: %v", err)
	}

	// Wait for processing
	time.Sleep(5 * time.Second)

	// Check request state
	state, err := node.GetRequestState(requestID)
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	// Even with divergent responses, we should still get a final consensus
	if state.Status == StatusFailed {
		t.Error("Request processing failed")
	}
	if state.ConsensusData == nil {
		t.Error("No consensus reached")
	}
}

func TestOracleNodeStateTracking(t *testing.T) {
	node := setupTestNode(t)

	// Test non-existent request
	_, err := node.GetRequestState("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent request, got nil")
	}

	// Test state transitions
	ctx := context.Background()
	requestID := "test-semantic-3"
	prompt := "What is the meaning of life?"

	err = node.ProcessPrompt(ctx, requestID, prompt)
	if err != nil {
		t.Fatalf("Failed to process prompt: %v", err)
	}

	// Wait for initial processing
	time.Sleep(1 * time.Second)

	// Check intermediate state
	state, err := node.GetRequestState(requestID)
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	if state.Status != StatusProcessing && state.Status != StatusConsensus {
		t.Errorf("Expected status Processing or Consensus, got %s", state.Status)
	}

	// Wait for completion
	time.Sleep(4 * time.Second)

	// Check final state
	state, err = node.GetRequestState(requestID)
	if err != nil {
		t.Fatalf("Failed to get request state: %v", err)
	}

	if state.Status != StatusComplete && state.Status != StatusConsensus {
		t.Errorf("Expected status Complete or Consensus, got %s", state.Status)
	}
}
