package node

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"oracle/internal/consensus"
	"oracle/internal/logging"
	"oracle/internal/network"
)

const defaultTimeout = 5 * time.Second

// hash returns a simple hash of a string
func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

type OracleNode struct {
	mu               sync.RWMutex
	nodeID           string
	address          string
	logger           *logging.Logger
	consensusEngine  *consensus.PBFT
	networkTransport network.Transport
	isRunning        bool
	peers            map[string]string // peerID -> address

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

func NewOracleNode(nodeID string, address string, logger *logging.Logger) (*OracleNode, error) {
	transport := network.NewP2PTransport(nodeID, address)

	node := &OracleNode{
		nodeID:           nodeID,
		address:          address,
		logger:           logger,
		networkTransport: transport,
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
	n.consensusEngine = consensus.NewPBFT(n.nodeID, nodes, defaultTimeout)

	// Set up network manager
	networkManager := consensus.NewNetworkManager(n.networkTransport, n.consensusEngine)
	n.consensusEngine.SetNetworkManager(networkManager)
}

// GetConsensus returns the consensus engine
func (n *OracleNode) GetConsensus() *consensus.PBFT {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.consensusEngine
}

// GetPeers returns a list of peer IDs
func (n *OracleNode) GetPeers() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	peers := make([]string, 0, len(n.peers))
	for peerID := range n.peers {
		peers = append(peers, peerID)
	}
	return peers
}

// RegisterPeer adds a peer to the node's peer list
func (n *OracleNode) RegisterPeer(peerID, address string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[peerID] = address
	return nil
}

// Start initializes and starts the node
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
