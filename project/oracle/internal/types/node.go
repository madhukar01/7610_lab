package node

import (
	"context"
	"fmt"
	"sync"
)

// NodeState represents the current state of the node
type NodeState int

const (
	StateInitial NodeState = iota
	StateRunning
	StateStopped
)

// OracleRequest represents a request to the oracle
type OracleRequest struct {
	RequestID string `json:"request_id"`
	Query     string `json:"query"`
	Callback  string `json:"callback"`
}

// OracleResponse represents a response from the oracle
type OracleResponse struct {
	RequestID string `json:"request_id"`
	Response  []byte `json:"response"`
	Error     error  `json:"error,omitempty"`
}

// Node represents an oracle node in the network
type Node struct {
	ID     string
	State  NodeState
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewNode creates a new oracle node instance
func NewNode(ctx context.Context, nodeID string) *Node {
	ctx, cancel := context.WithCancel(ctx)
	return &Node{
		ID:     nodeID,
		State:  StateInitial,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start initializes and starts the node
func (n *Node) Start() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.State != StateInitial {
		return fmt.Errorf("node already started")
	}

	// TODO: Initialize components
	n.State = StateRunning
	return nil
}

// Stop gracefully stops the node
func (n *Node) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.State != StateRunning {
		return fmt.Errorf("node not running")
	}

	n.cancel()
	n.State = StateStopped
	return nil
}
