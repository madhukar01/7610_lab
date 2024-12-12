package tests

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/mhollas/7610/agent/llm"
	"github.com/mhollas/7610/oracle/pkg/logging"
	"github.com/mhollas/7610/oracle/pkg/node"
	"github.com/mhollas/7610/oracle/pkg/similarity"
	"github.com/mhollas/7610/oracle/pkg/storage"
)

// MockLLMClient implements a mock LLM client for testing
type MockLLMClient struct{}

func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{}
}

func (m *MockLLMClient) GetResponse(ctx context.Context, request *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{
		Text:       "Mock response for: " + request.Prompt,
		TokensUsed: 10,
		Model:      "mock-model",
		Timestamp:  time.Now(),
		RequestID:  request.RequestID,
		NodeID:     request.NodeID,
		ExtraParams: map[string]interface{}{
			"mock": true,
		},
	}, nil
}

func (m *MockLLMClient) ValidateResponse(response *llm.LLMResponse) error {
	if response.Text == "" {
		return fmt.Errorf("empty response text")
	}
	return nil
}

func (m *MockLLMClient) GetModelInfo() map[string]interface{} {
	return map[string]interface{}{
		"model": "mock-model",
	}
}

// MockStorageClient implements a mock storage client for testing
type MockStorageClient struct {
	records map[string]*storage.ExecutionRecord
}

func NewMockStorageClient() *MockStorageClient {
	return &MockStorageClient{
		records: make(map[string]*storage.ExecutionRecord),
	}
}

func (m *MockStorageClient) StoreExecutionRecord(ctx context.Context, record *storage.ExecutionRecord) (string, error) {
	cid := fmt.Sprintf("mock-cid-%s", record.RequestID)
	m.records[cid] = record
	return cid, nil
}

func (m *MockStorageClient) GetExecutionRecord(ctx context.Context, cid string) (*storage.ExecutionRecord, error) {
	record, exists := m.records[cid]
	if !exists {
		return nil, fmt.Errorf("record not found: %s", cid)
	}
	return record, nil
}

// MockSemanticScorer implements a mock semantic scorer for testing
type MockSemanticScorer struct{}

func NewMockSemanticScorer() *MockSemanticScorer {
	return &MockSemanticScorer{}
}

func (m *MockSemanticScorer) ClusterResponses(ctx context.Context, responses []*similarity.Response, threshold float32) ([]*similarity.Cluster, error) {
	if len(responses) == 0 {
		return nil, fmt.Errorf("no responses to cluster")
	}

	// Put all responses in a single cluster for testing
	cluster := &similarity.Cluster{
		Responses:    responses,
		AverageScore: 0.95,
	}

	return []*similarity.Cluster{cluster}, nil
}

func (m *MockSemanticScorer) SelectConsensusResponse(clusters []*similarity.Cluster) *similarity.Response {
	if len(clusters) == 0 || len(clusters[0].Responses) == 0 {
		return nil
	}
	return clusters[0].Responses[0]
}

// TestNode wraps an OracleNode with additional testing functionality
type TestNode struct {
	*node.OracleNode
	nodeID string
}

func (n *TestNode) GetID() string {
	return n.nodeID
}

// CreateTestNode creates a node for testing
func CreateTestNode(nodeID string, peers []string) (*TestNode, error) {
	// Create logger
	logger := logging.NewLogger(logging.DEBUG, os.Stdout, "test")
	addr := fmt.Sprintf("localhost:%s", GetFreePort())

	// Create LLM client
	cfg := &llm.Config{
		Model:              "gpt-4o-mini",
		MaxTokens:          1000,
		DefaultTemperature: 0.7,
		MaxRetries:         3,
		RetryIntervalMs:    1000,
	}
	llmClient, err := llm.NewOpenAIClient("", cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %w", err)
	}

	// Create mock storage client (keep this mocked to avoid IPFS costs)
	storageClient := NewMockStorageClient()

	// Create semantic scorer
	scorer := similarity.NewSemanticScorer("", "")

	// Create node
	n, err := node.NewOracleNode(nodeID, addr, llmClient, storageClient, scorer, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create node: %w", err)
	}

	n.InitConsensus(peers)
	return &TestNode{
		OracleNode: n,
		nodeID:     nodeID,
	}, nil
}

// GetFreePort returns a free port number as a string
func GetFreePort() string {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "0"
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "0"
	}
	defer l.Close()

	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}
