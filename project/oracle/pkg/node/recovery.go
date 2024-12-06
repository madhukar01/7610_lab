package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/logging"
)

// Recovery handles saving and restoring node state
type Recovery struct {
	node      *OracleNode
	statePath string
	logger    *logging.Logger
}

// NewRecovery creates a new recovery handler
func NewRecovery(node *OracleNode, statePath string, logger *logging.Logger) *Recovery {
	return &Recovery{
		node:      node,
		statePath: statePath,
		logger:    logger,
	}
}

// SaveState saves the current node state to disk
func (r *Recovery) SaveState() error {
	state := map[string]interface{}{
		"sequence": r.node.consensus.GetSequence(),
		"view":     r.node.consensus.GetView(),
		"state":    int(r.node.consensus.GetState()),
	}

	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(r.statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// Recover restores node state from disk
func (r *Recovery) Recover(ctx context.Context) error {
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			r.logger.Info("No state file found, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	sequence := uint64(state["sequence"].(float64))
	view := uint64(state["view"].(float64))
	pbftState := consensus.PBFTState(int(state["state"].(float64)))

	if err := r.node.consensus.RestoreState(sequence, view, pbftState); err != nil {
		return fmt.Errorf("failed to restore state: %w", err)
	}

	return nil
}
