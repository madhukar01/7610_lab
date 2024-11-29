package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oracle/internal/consensus"
	"oracle/internal/logging"
)

type RecoveryState struct {
	LastSequence uint64              `json:"last_sequence"`
	ViewNumber   uint64              `json:"view_number"`
	Leader       string              `json:"leader"`
	Peers        []string            `json:"peers"`
	Timestamp    time.Time           `json:"timestamp"`
	State        consensus.PBFTState `json:"state"`
}

type Recovery struct {
	logger    *logging.Logger
	statePath string
	node      *OracleNode
}

func NewRecovery(node *OracleNode, statePath string, logger *logging.Logger) *Recovery {
	return &Recovery{
		logger:    logger,
		statePath: statePath,
		node:      node,
	}
}

func (r *Recovery) SaveState() error {
	r.node.mu.RLock()
	state := &RecoveryState{
		LastSequence: r.node.consensusEngine.GetSequence(),
		ViewNumber:   r.node.consensusEngine.GetView(),
		Leader:       r.node.consensusEngine.GetLeader(),
		Peers:        r.node.GetPeers(),
		Timestamp:    time.Now(),
		State:        r.node.consensusEngine.GetState(),
	}
	r.node.mu.RUnlock()

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal recovery state: %w", err)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(r.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Write state atomically using temporary file
	tmpFile := r.statePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}

	if err := os.Rename(tmpFile, r.statePath); err != nil {
		return fmt.Errorf("failed to rename temporary state file: %w", err)
	}

	r.logger.Info("Saved recovery state", state)
	return nil
}

func (r *Recovery) LoadState() (*RecoveryState, error) {
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			r.logger.Info("No recovery state found")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state RecoveryState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal recovery state: %w", err)
	}

	r.logger.Info("Loaded recovery state", state)
	return &state, nil
}

func (r *Recovery) Recover(ctx context.Context) error {
	state, err := r.LoadState()
	if err != nil {
		return fmt.Errorf("failed to load recovery state: %w", err)
	}

	if state == nil {
		r.logger.Info("No recovery state found, starting fresh")
		return nil
	}

	// Verify state timestamp isn't too old
	if time.Since(state.Timestamp) > 1*time.Hour {
		r.logger.Warn("Recovery state is too old, starting fresh")
		return nil
	}

	// Restore consensus state
	r.node.mu.Lock()
	if err := r.node.consensusEngine.RestoreState(state.LastSequence, state.ViewNumber, state.State); err != nil {
		r.node.mu.Unlock()
		return fmt.Errorf("failed to restore consensus state: %w", err)
	}
	r.node.mu.Unlock()

	// Reconnect to peers
	for _, peerID := range state.Peers {
		// Get peer address from the node's peer map
		r.node.mu.RLock()
		addr, exists := r.node.peers[peerID]
		r.node.mu.RUnlock()

		if !exists {
			r.logger.Warn("Peer address not found", map[string]interface{}{
				"peer": peerID,
			})
			continue
		}

		if err := r.node.networkTransport.RegisterPeer(peerID, addr); err != nil {
			r.logger.Warn("Failed to reconnect to peer", map[string]interface{}{
				"peer":    peerID,
				"address": addr,
				"error":   err.Error(),
			})
		}
	}

	r.logger.Info("Recovery completed successfully", state)
	return nil
}
