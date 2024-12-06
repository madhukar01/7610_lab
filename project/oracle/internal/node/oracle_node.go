package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"agent/src/llm"
	"oracle/internal/consensus"
	"oracle/internal/similarity"
	"oracle/internal/storage"
)

// OracleNode represents a single node in the oracle network
type OracleNode struct {
	nodeID    string
	llmClient *llm.OpenAIClient
	storage   storage.StorageClient
	consensus *consensus.PBFT
	scorer    *similarity.SemanticScorer
	responses map[string]*RequestState
	mu        sync.RWMutex
}

// RequestState tracks the state of a prompt request
type RequestState struct {
	Prompt        string
	LLMResponse   *llm.LLMResponse
	Responses     map[string]*llm.LLMResponse // nodeID -> response
	ConsensusData *consensus.ConsensusResult
	Clusters      []*similarity.Cluster
	IPFSCID       string
	StartTime     time.Time
	Status        RequestStatus
}

type RequestStatus string

const (
	StatusPending    RequestStatus = "pending"
	StatusProcessing RequestStatus = "processing"
	StatusConsensus  RequestStatus = "consensus"
	StatusComplete   RequestStatus = "complete"
	StatusFailed     RequestStatus = "failed"
)

// NewOracleNode creates a new oracle node
func NewOracleNode(nodeID string, llmClient *llm.OpenAIClient, storage storage.StorageClient, scorer *similarity.SemanticScorer, nodes []string) (*OracleNode, error) {
	// Create PBFT consensus instance
	pbft := consensus.NewPBFT(nodeID, nodes, 5*time.Second)

	node := &OracleNode{
		nodeID:    nodeID,
		llmClient: llmClient,
		storage:   storage,
		consensus: pbft,
		scorer:    scorer,
		responses: make(map[string]*RequestState),
	}

	// Set consensus callback
	pbft.SetResultCallback(node.handleConsensusResult)

	return node, nil
}

// ProcessPrompt handles a new prompt request
func (n *OracleNode) ProcessPrompt(ctx context.Context, requestID string, prompt string) error {
	n.mu.Lock()
	if _, exists := n.responses[requestID]; exists {
		n.mu.Unlock()
		return fmt.Errorf("request %s already exists", requestID)
	}

	// Initialize request state
	n.responses[requestID] = &RequestState{
		Prompt:    prompt,
		Responses: make(map[string]*llm.LLMResponse),
		StartTime: time.Now(),
		Status:    StatusProcessing,
	}
	n.mu.Unlock()

	// Get LLM response
	llmResp, err := n.getLLMResponse(ctx, requestID, prompt)
	if err != nil {
		n.setRequestStatus(requestID, StatusFailed)
		return fmt.Errorf("failed to get LLM response: %w", err)
	}

	// Store own response
	n.mu.Lock()
	n.responses[requestID].LLMResponse = llmResp
	n.responses[requestID].Responses[n.nodeID] = llmResp
	n.mu.Unlock()

	// Convert responses for semantic scoring
	semanticResponses := make([]*similarity.Response, 0)
	for nodeID, resp := range n.responses[requestID].Responses {
		semanticResponses = append(semanticResponses, &similarity.Response{
			Text:   resp.Text,
			NodeID: nodeID,
		})
	}

	// Get semantic clusters
	clusters, err := n.scorer.ClusterResponses(ctx, semanticResponses, 0.85)
	if err != nil {
		n.setRequestStatus(requestID, StatusFailed)
		return fmt.Errorf("failed to cluster responses: %w", err)
	}

	// Store clusters
	n.mu.Lock()
	n.responses[requestID].Clusters = clusters
	n.mu.Unlock()

	// Select consensus response
	consensusResp := n.scorer.SelectConsensusResponse(clusters)
	if consensusResp == nil {
		n.setRequestStatus(requestID, StatusFailed)
		return fmt.Errorf("failed to select consensus response")
	}

	// Prepare consensus data
	consensusData := &consensus.ConsensusData{
		RequestID: requestID,
		NodeID:    n.nodeID,
		Response:  n.responses[requestID].Responses[consensusResp.NodeID],
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(consensusData)
	if err != nil {
		return fmt.Errorf("failed to marshal consensus data: %w", err)
	}

	// Start consensus process
	n.setRequestStatus(requestID, StatusConsensus)
	if err := n.consensus.ProposeValue(data); err != nil {
		n.setRequestStatus(requestID, StatusFailed)
		return fmt.Errorf("failed to propose value: %w", err)
	}

	return nil
}

// handleConsensusResult handles the consensus result
func (n *OracleNode) handleConsensusResult(result consensus.ConsensusResult) {
	var consensusData consensus.ConsensusData
	if err := json.Unmarshal(result.Data, &consensusData); err != nil {
		log.Printf("Failed to unmarshal consensus data: %v", err)
		return
	}

	// Store the consensus result
	n.mu.Lock()
	state := n.responses[consensusData.RequestID]
	state.ConsensusData = &result
	n.mu.Unlock()

	// Create execution record for IPFS
	record := &storage.ExecutionRecord{
		RequestID:       consensusData.RequestID,
		RequestInput:    state.Prompt,
		Timestamp:       time.Now(),
		OracleResponses: make([]storage.OracleResponse, 0, len(state.Responses)),
		Consensus: storage.ConsensusData{
			Method:           "Semantic-PBFT",
			ParticipantCount: len(state.Responses),
			AgreementScore:   state.Clusters[0].AverageScore,
			Round:            1,
		},
		FinalResponse: consensusData.Response.Text,
	}

	// Add all responses
	for nodeID, resp := range state.Responses {
		record.OracleResponses = append(record.OracleResponses, storage.OracleResponse{
			NodeID:      nodeID,
			LLMResponse: resp.Text,
			Timestamp:   resp.Timestamp,
			Metadata:    resp.ExtraParams,
		})
	}

	// Store in IPFS
	ctx := context.Background()
	cid, err := n.storage.StoreExecutionRecord(ctx, record)
	if err != nil {
		log.Printf("Failed to store execution record: %v", err)
		n.setRequestStatus(consensusData.RequestID, StatusFailed)
		return
	}

	// Update request state
	n.mu.Lock()
	state.IPFSCID = cid
	state.Status = StatusComplete
	n.mu.Unlock()
}

// getLLMResponse gets response from LLM
func (n *OracleNode) getLLMResponse(ctx context.Context, requestID string, prompt string) (*llm.LLMResponse, error) {
	req := &llm.LLMRequest{
		Prompt:      prompt,
		Temperature: 0.7,
		RequestID:   requestID,
		NodeID:      n.nodeID,
		ExtraParams: map[string]interface{}{
			"timestamp": time.Now(),
		},
	}

	return n.llmClient.GetResponse(ctx, req)
}

// setRequestStatus updates the request status
func (n *OracleNode) setRequestStatus(requestID string, status RequestStatus) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if state, exists := n.responses[requestID]; exists {
		state.Status = status
	}
}

// GetRequestState returns the current state of a request
func (n *OracleNode) GetRequestState(requestID string) (*RequestState, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	state, exists := n.responses[requestID]
	if !exists {
		return nil, fmt.Errorf("request %s not found", requestID)
	}
	return state, nil
}
