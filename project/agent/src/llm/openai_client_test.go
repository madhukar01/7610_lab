package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

func getTestConfig() *Config {
	return &Config{
		Model:              "gpt-4-1106-preview",
		MaxTokens:          1000,
		DefaultTemperature: 0.7,
		MaxRetries:         3,
		RetryIntervalMs:    1000,
	}
}

func TestOpenAIClient(t *testing.T) {
	// Skip if no API key is provided
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	// Create client with test config
	client, err := NewOpenAIClient(apiKey, getTestConfig())
	if err != nil {
		t.Fatalf("Failed to create OpenAI client: %v", err)
	}

	// Test request
	req := &LLMRequest{
		Prompt:      "What is 2+2?",
		Temperature: 0.7,
		RequestID:   "test-req-1",
		NodeID:      "test-node-1",
		ExtraParams: map[string]interface{}{
			"purpose": "testing",
		},
	}

	// Test getting response
	ctx := context.Background()
	resp, err := client.GetResponse(ctx, req)
	if err != nil {
		t.Fatalf("Failed to get response: %v", err)
	}

	// Validate response
	if err := client.ValidateResponse(resp); err != nil {
		t.Fatalf("Invalid response: %v", err)
	}

	// Check response fields
	if resp.Text == "" {
		t.Error("Empty response text")
	}
	if resp.TokensUsed == 0 {
		t.Error("No tokens used")
	}
	if resp.RequestID != req.RequestID {
		t.Errorf("Expected RequestID %s, got %s", req.RequestID, resp.RequestID)
	}
	if resp.NodeID != req.NodeID {
		t.Errorf("Expected NodeID %s, got %s", req.NodeID, resp.NodeID)
	}

	// Test model info
	info := client.GetModelInfo()
	if info["model"] != getTestConfig().Model {
		t.Errorf("Expected model %s, got %s", getTestConfig().Model, info["model"])
	}
	if info["max_tokens"].(int) != getTestConfig().MaxTokens {
		t.Errorf("Expected max_tokens %d, got %d", getTestConfig().MaxTokens, info["max_tokens"])
	}
}

func TestOpenAIClientValidation(t *testing.T) {
	client := &OpenAIClient{
		cfg: getTestConfig(),
	}

	// Test invalid response
	invalidResp := &LLMResponse{
		Text:      "",
		NodeID:    "",
		RequestID: "",
	}
	if err := client.ValidateResponse(invalidResp); err == nil {
		t.Error("Expected validation error for empty response")
	}

	// Test valid response
	validResp := &LLMResponse{
		Text:       "Test response",
		TokensUsed: 10,
		Model:      "test-model",
		Timestamp:  time.Now(),
		RequestID:  "test-req",
		NodeID:     "test-node",
		ExtraParams: map[string]interface{}{
			"test": "value",
		},
	}
	if err := client.ValidateResponse(validResp); err != nil {
		t.Errorf("Unexpected validation error: %v", err)
	}
}

func TestNewOpenAIClientValidation(t *testing.T) {
	// Test nil config
	_, err := NewOpenAIClient("test-key", nil)
	if err == nil {
		t.Error("Expected error for nil config")
	}

	// Test valid config
	client, err := NewOpenAIClient("test-key", getTestConfig())
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if client == nil {
		t.Error("Expected non-nil client")
	}
}
