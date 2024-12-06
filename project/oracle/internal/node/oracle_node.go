package node

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"agent/src/llm"
	"oracle/internal/consensus"
	"oracle/internal/logging"
	"oracle/internal/network"
	"oracle/internal/similarity"
	"oracle/internal/storage"
)

const (
	defaultTimeout = 5 * time.Second
)

// OracleNode represents a single node in the oracle network
type OracleNode struct {
	mu               sync.RWMutex
	nodeID           string
	address          string
	llmClient        *llm.OpenAIClient
	storage          storage.StorageClient
	consensus        *consensus.PBFT
	networkTransport network.Transport
	scorer           *similarity.SemanticScorer
	requests         map[string]*RequestState
	isRunning        bool
	peers            map[string]string // peerID -> address
	logger           *logging.Logger
	messageCallback  func(*consensus.ConsensusMessage)

	// Channels for request handling
	requestChan  chan *OracleRequest
	responseChan chan *OracleResponse
}

// NewOracleNode creates a new oracle node
func NewOracleNode(nodeID string, address string, llmClient *llm.OpenAIClient, storage storage.StorageClient, scorer *similarity.SemanticScorer, logger *logging.Logger) (*OracleNode, error) {
	transport := network.NewP2PTransport(nodeID, address)

	node := &OracleNode{
		nodeID:           nodeID,
		address:          address,
		llmClient:        llmClient,
		storage:          storage,
		scorer:           scorer,
		logger:           logger,
		networkTransport: transport,
		requests:         make(map[string]*RequestState),
		peers:            make(map[string]string),
		requestChan:      make(chan *OracleRequest, 1000),
		responseChan:     make(chan *OracleResponse, 1000),
	}

	return node, nil
}

// InitConsensus initializes the consensus engine
func (n *OracleNode) InitConsensus(nodes []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.consensus = consensus.NewPBFT(n.nodeID, nodes, defaultTimeout)

	// Set up network manager
	networkManager := consensus.NewNetworkManager(n.networkTransport, n.consensus)
	n.consensus.SetNetworkManager(networkManager)
	n.consensus.SetResultCallback(n.handleConsensusResult)
}

// ProcessPrompt handles a new prompt request
func (n *OracleNode) ProcessPrompt(ctx context.Context, requestID string, prompt string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Create request state
	state := &RequestState{
		Status:    "pending",
		RequestID: requestID,
		Prompt:    prompt,
		StartTime: time.Now(),
		NodeID:    n.nodeID,
		Responses: make(map[string]*llm.LLMResponse),
		Clusters:  nil,
		IPFSCID:   "",
	}
	n.requests[requestID] = state

	// Only leader proposes values
	if !n.consensus.IsLeader() {
		n.logger.Debug("Non-leader node waiting for consensus messages")
		return nil
	}

	// Get LLM response
	req := &llm.LLMRequest{
		Prompt:      prompt,
		Temperature: 0.7,
		RequestID:   requestID,
		NodeID:      n.nodeID,
		ExtraParams: map[string]interface{}{
			"timestamp": time.Now(),
		},
	}
	resp, err := n.llmClient.GetResponse(ctx, req)
	if err != nil {
		state.Status = "failed"
		state.Error = err
		return fmt.Errorf("failed to get LLM response: %w", err)
	}

	// Store response
	state.LLMResponse = resp
	state.Responses[n.nodeID] = resp

	// Create consensus data
	consensusData := &consensus.ConsensusData{
		RequestID: requestID,
		NodeID:    n.nodeID,
		Response:  resp,
		Timestamp: time.Now(),
	}

	// Marshal consensus data
	dataBytes, err := json.Marshal(consensusData)
	if err != nil {
		state.Status = "failed"
		state.Error = err
		return fmt.Errorf("failed to marshal consensus data: %w", err)
	}

	// Start consensus process
	if err := n.consensus.ProposeValue(dataBytes); err != nil {
		state.Status = "failed"
		state.Error = err
		return fmt.Errorf("failed to propose value: %w", err)
	}

	state.Status = "consensus"
	return nil
}

// handleConsensusResult handles the consensus result
func (n *OracleNode) handleConsensusResult(result consensus.ConsensusResult) {
	n.mu.Lock()
	defer n.mu.Unlock()

	var consensusData consensus.ConsensusData
	if err := json.Unmarshal(result.Data, &consensusData); err != nil {
		n.logger.Error("Failed to unmarshal consensus data:", err)
		return
	}

	state, exists := n.requests[consensusData.RequestID]
	if !exists {
		n.logger.Error("Request state not found:", consensusData.RequestID)
		return
	}

	// Update state with consensus result
	state.Status = "complete"
	state.ConsensusData = &consensusData

	// Store response if not already present
	if _, exists := state.Responses[consensusData.NodeID]; !exists {
		state.Responses[consensusData.NodeID] = consensusData.Response
	}

	// Perform semantic clustering
	if len(state.Responses) > 0 {
		responses := make([]*similarity.Response, 0, len(state.Responses))
		for _, resp := range state.Responses {
			responses = append(responses, &similarity.Response{
				NodeID: resp.NodeID,
				Text:   resp.Text,
			})
		}

		clusters, err := n.scorer.ClusterResponses(context.Background(), responses, 0.85)
		if err != nil {
			n.logger.Error("Failed to cluster responses:", err)
		} else {
			state.Clusters = clusters
		}
	}

	// Store final result in IPFS
	record := &storage.ExecutionRecord{
		RequestID:     consensusData.RequestID,
		RequestInput:  state.Prompt,
		Timestamp:     time.Now(),
		FinalResponse: consensusData.Response.Text,
	}

	cid, err := n.storage.StoreExecutionRecord(context.Background(), record)
	if err != nil {
		n.logger.Error("Failed to store result:", err)
	} else {
		state.IPFSCID = cid
	}
}

// GetRequestState returns the current state of a request
func (n *OracleNode) GetRequestState(requestID string) (*RequestState, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	state, exists := n.requests[requestID]
	if !exists {
		return nil, fmt.Errorf("request %s not found", requestID)
	}
	return state, nil
}

// Start starts the oracle node
func (n *OracleNode) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.isRunning {
		n.mu.Unlock()
		return fmt.Errorf("node is already running")
	}
	n.isRunning = true
	n.mu.Unlock()

	// Start consensus engine
	if err := n.consensus.Start(ctx); err != nil {
		return fmt.Errorf("failed to start consensus: %w", err)
	}

	// Start network transport
	if err := n.networkTransport.Start(ctx); err != nil {
		return fmt.Errorf("failed to start network transport: %w", err)
	}

	// Start message handling
	go n.handleMessages(ctx)

	return nil
}

// handleMessages processes incoming network messages
func (n *OracleNode) handleMessages(ctx context.Context) {
	msgChan := n.networkTransport.Receive()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-msgChan:
			if msg == nil {
				continue
			}

			// Handle consensus message
			var consensusMsg consensus.ConsensusMessage
			if err := json.Unmarshal(msg.Payload, &consensusMsg); err != nil {
				n.logger.Error("Failed to unmarshal consensus message:", err)
				continue
			}

			// Call message callback if set
			n.mu.RLock()
			if n.messageCallback != nil {
				n.messageCallback(&consensusMsg)
			}
			n.mu.RUnlock()

			// Extract consensus data
			var consensusData consensus.ConsensusData
			if err := json.Unmarshal(consensusMsg.Data, &consensusData); err != nil {
				n.logger.Error("Failed to unmarshal consensus data:", err)
				continue
			}

			// Create request state if it doesn't exist
			n.mu.Lock()
			if _, exists := n.requests[consensusData.RequestID]; !exists {
				n.requests[consensusData.RequestID] = &RequestState{
					Status:    "consensus",
					RequestID: consensusData.RequestID,
					NodeID:    n.nodeID,
					StartTime: time.Now(),
					Responses: make(map[string]*llm.LLMResponse),
				}
			}
			n.mu.Unlock()

			// Process consensus message
			if err := n.consensus.ProcessMessage(&consensusMsg); err != nil {
				n.logger.Error("Failed to process consensus message:", err)
				continue
			}
		}
	}
}

// Stop stops the oracle node
func (n *OracleNode) Stop() error {
	n.mu.Lock()
	if !n.isRunning {
		n.mu.Unlock()
		return nil
	}
	n.isRunning = false
	n.mu.Unlock()

	// Stop consensus engine
	if err := n.consensus.Stop(); err != nil {
		return fmt.Errorf("failed to stop consensus: %w", err)
	}

	// Stop network transport
	if err := n.networkTransport.Stop(); err != nil {
		return fmt.Errorf("failed to stop network transport: %w", err)
	}

	return nil
}

// RegisterPeer registers a peer node
func (n *OracleNode) RegisterPeer(peerID string, address string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.peers[peerID] = address
	return n.networkTransport.RegisterPeer(peerID, address)
}

// GetNodeID returns the node's ID
func (n *OracleNode) GetNodeID() string {
	return n.nodeID
}

// SetMessageCallback sets a callback for consensus message handling
func (n *OracleNode) SetMessageCallback(callback func(*consensus.ConsensusMessage)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.messageCallback = callback
}

// GetMessageCallback returns the current message callback
func (n *OracleNode) GetMessageCallback() func(*consensus.ConsensusMessage) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.messageCallback
}
