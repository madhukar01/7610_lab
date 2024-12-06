package consensus

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/mhollas/7610/oracle/pkg/network"
)

// NetworkManager handles network communication for PBFT
type NetworkManager struct {
	transport network.Transport
	pbft      *PBFT
}

// NewNetworkManager creates a new network manager
func NewNetworkManager(transport network.Transport, pbft *PBFT) *NetworkManager {
	return &NetworkManager{
		transport: transport,
		pbft:      pbft,
	}
}

// Start starts the network manager
func (nm *NetworkManager) Start() {
	go nm.handleMessages()
}

// handleMessages processes incoming network messages
func (nm *NetworkManager) handleMessages() {
	for msg := range nm.transport.Receive() {
		if err := nm.handleMessage(msg); err != nil {
			log.Printf("Failed to handle message: %v", err)
		}
	}
}

// handleMessage processes a single network message
func (nm *NetworkManager) handleMessage(msg *network.Message) error {
	// Decode consensus message
	var consensusMsg ConsensusMessage
	if err := json.Unmarshal(msg.Payload, &consensusMsg); err != nil {
		return fmt.Errorf("failed to unmarshal consensus message: %w", err)
	}

	// Process the message
	return nm.pbft.ProcessMessage(&consensusMsg)
}

// Broadcast sends a consensus message to all peers
func (nm *NetworkManager) Broadcast(msg *ConsensusMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal consensus message: %w", err)
	}

	return nm.transport.Broadcast(data)
}
