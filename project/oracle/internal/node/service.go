package node

import (
	"context"
	"fmt"
	"sync"

	"oracle/internal/consensus"
	"oracle/internal/network"
)

type OracleNode struct {
	mu               sync.RWMutex
	nodeID           string
	consensusEngine  *consensus.PBFT
	networkTransport network.Transport
	isRunning        bool

	// Channels for request handling
	requestChan  chan *OracleRequest
	responseChan chan *OracleResponse
}

type OracleRequest struct {
	RequestID string
	Query     string
	Callback  string
}

type OracleResponse struct {
	RequestID string
	Response  []byte
	Error     error
}

func NewOracleNode(nodeID string, peers []string) (*OracleNode, error) {
	transport := network.NewP2PTransport(nodeID, fmt.Sprintf(":%d", 8000+hash(nodeID)%1000))
	pbft := consensus.NewPBFT(nodeID, peers, defaultTimeout)

	node := &OracleNode{
		nodeID:           nodeID,
		consensusEngine:  pbft,
		networkTransport: transport,
		requestChan:      make(chan *OracleRequest, 1000),
		responseChan:     make(chan *OracleResponse, 1000),
	}

	// Set up network manager
	networkManager := consensus.NewNetworkManager(transport, pbft)
	pbft.SetNetworkManager(networkManager)

	return node, nil
}

func (n *OracleNode) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.isRunning {
		n.mu.Unlock()
		return fmt.Errorf("node already running")
	}
	n.isRunning = true
	n.mu.Unlock()

	// Start network transport
	if err := n.networkTransport.Start(ctx); err != nil {
		return fmt.Errorf("failed to start network transport: %w", err)
	}

	// Start consensus engine
	if err := n.consensusEngine.Start(ctx); err != nil {
		return fmt.Errorf("failed to start consensus engine: %w", err)
	}

	// Start request handling
	go n.handleRequests(ctx)

	return nil
}

func (n *OracleNode) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.isRunning {
		return nil
	}

	n.isRunning = false

	// Stop consensus engine
	if err := n.consensusEngine.Stop(); err != nil {
		return fmt.Errorf("failed to stop consensus engine: %w", err)
	}

	// Stop network transport
	if err := n.networkTransport.Stop(); err != nil {
		return fmt.Errorf("failed to stop network transport: %w", err)
	}

	return nil
}

func (n *OracleNode) handleRequests(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-n.requestChan:
			go n.processRequest(ctx, req)
		}
	}
}

func (n *OracleNode) processRequest(ctx context.Context, req *OracleRequest) {
	// TODO: Implement request processing logic
	// 1. Validate request
	// 2. Propose to consensus
	// 3. Wait for consensus result
	// 4. Send response
}

func (n *OracleNode) SubmitRequest(req *OracleRequest) error {
	select {
	case n.requestChan <- req:
		return nil
	default:
		return fmt.Errorf("request channel full")
	}
}
