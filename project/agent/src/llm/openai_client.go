package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/sashabaranov/go-openai"
)

// LLMRequest represents a request to the LLM
type LLMRequest struct {
	Prompt      string                 `json:"prompt"`
	Temperature float32                `json:"temperature"`
	ExtraParams map[string]interface{} `json:"extra_params"`
	RequestID   string                 `json:"request_id"`
	NodeID      string                 `json:"node_id"`
}

// LLMResponse represents a response from the LLM
type LLMResponse struct {
	Text        string                 `json:"text"`
	TokensUsed  int                    `json:"tokens_used"`
	Model       string                 `json:"model"`
	Timestamp   time.Time              `json:"timestamp"`
	RequestID   string                 `json:"request_id"`
	NodeID      string                 `json:"node_id"`
	ExtraParams map[string]interface{} `json:"extra_params"`
}

// OpenAIClient handles communication with OpenAI's API
type OpenAIClient struct {
	client *openai.Client
	cfg    *Config
}

// Config holds the OpenAI client configuration from global config
type Config struct {
	Model              string
	MaxTokens          int
	DefaultTemperature float32
	MaxRetries         int
	RetryIntervalMs    int
}

// NewOpenAIClient creates a new OpenAI client using global config
func NewOpenAIClient(apiKey string, cfg *Config) (*OpenAIClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	client := openai.NewClient(apiKey)
	return &OpenAIClient{
		client: client,
		cfg:    cfg,
	}, nil
}

// GetResponse gets a response from the LLM
func (c *OpenAIClient) GetResponse(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	// Create OpenAI request
	req := openai.ChatCompletionRequest{
		Model:       c.cfg.Model,
		MaxTokens:   c.cfg.MaxTokens,
		Temperature: request.Temperature,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: request.Prompt,
			},
		},
	}

	// Call OpenAI API
	resp, err := c.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenAI response: %w", err)
	}

	// Create response
	llmResp := &LLMResponse{
		Text:       resp.Choices[0].Message.Content,
		TokensUsed: resp.Usage.TotalTokens,
		Model:      resp.Model,
		Timestamp:  time.Now(),
		RequestID:  request.RequestID,
		NodeID:     request.NodeID,
		ExtraParams: map[string]interface{}{
			"prompt_tokens":     resp.Usage.PromptTokens,
			"completion_tokens": resp.Usage.CompletionTokens,
			"temperature":       request.Temperature,
		},
	}

	return llmResp, nil
}

// ValidateResponse validates the LLM response
func (c *OpenAIClient) ValidateResponse(response *LLMResponse) error {
	if response.Text == "" {
		return fmt.Errorf("empty response text")
	}
	if response.TokensUsed == 0 {
		return fmt.Errorf("no tokens used")
	}
	if response.RequestID == "" {
		return fmt.Errorf("missing request ID")
	}
	if response.NodeID == "" {
		return fmt.Errorf("missing node ID")
	}
	return nil
}

// GetModelInfo returns information about the current model
func (c *OpenAIClient) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model":             c.cfg.Model,
		"max_tokens":        c.cfg.MaxTokens,
		"default_temp":      c.cfg.DefaultTemperature,
		"max_retries":       c.cfg.MaxRetries,
		"retry_interval_ms": c.cfg.RetryIntervalMs,
	}
}
