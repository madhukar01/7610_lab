package consensus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Add these new error types at the top
var (
	ErrInvalidMessage  = fmt.Errorf("invalid message")
	ErrInvalidSender   = fmt.Errorf("invalid sender")
	ErrInvalidSequence = fmt.Errorf("invalid sequence number")
	ErrInvalidView     = fmt.Errorf("invalid view number")
)

// NewPBFT creates a new PBFT consensus engine
func NewPBFT(nodeID string, nodes []string, timeout time.Duration) *PBFT {
	pbft := &PBFT{
		nodeID:             nodeID,
		nodes:              nodes,
		timeout:            timeout,
		view:               0,
		sequence:           0,
		state:              StateNormal,
		prepareMessages:    make(map[uint64]map[string]*ConsensusMessage),
		commitMessages:     make(map[uint64]map[string]*ConsensusMessage),
		viewChangeMsgs:     make(map[uint64]map[string]*ConsensusMessage),
		msgChan:            make(chan *ConsensusMessage, 1000),
		doneChan:           make(chan struct{}),
		f:                  (len(nodes) - 1) / 3,
		checkpoints:        make(map[uint64][]byte),
		checkpointInterval: 100,
		consensusReached:   make(map[uint64]bool),
	}

	// Set initial leader (node0)
	pbft.isLeader = nodeID == nodes[0]

	return pbft
}

func (p *PBFT) Start(ctx context.Context) error {
	go p.run(ctx)
	return nil
}

func (p *PBFT) Stop() error {
	close(p.doneChan)
	return nil
}

func (p *PBFT) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-p.msgChan:
			p.handleMessage(msg)
		}
	}
}

func (p *PBFT) ProposeValue(value []byte) error {
	if !p.isLeader {
		return fmt.Errorf("node is not the leader")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Compute message digest
	digest := p.computeDigest(value)

	// Create pre-prepare message
	msg := &ConsensusMessage{
		Type:     PrePrepare,
		NodeID:   p.nodeID,
		View:     p.view,
		Sequence: p.sequence,
		Digest:   digest,
		Data:     value,
	}

	// Store message for validation
	if _, exists := p.prepareMessages[p.sequence]; !exists {
		p.prepareMessages[p.sequence] = make(map[string]*ConsensusMessage)
	}
	p.prepareMessages[p.sequence][p.nodeID] = msg

	// Broadcast pre-prepare message
	if err := p.broadcast(msg); err != nil {
		return fmt.Errorf("failed to broadcast pre-prepare: %w", err)
	}

	// Increment sequence number
	p.sequence++
	return nil
}

func (p *PBFT) ProcessMessage(msg *ConsensusMessage) error {
	if err := p.validateMessage(msg); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
	}

	// Update sequence if needed
	if msg.Sequence > p.sequence {
		p.sequence = msg.Sequence
	}

	// Update view if needed
	if msg.View > p.view {
		p.view = msg.View
	}

	p.msgChan <- msg
	return nil
}

func (p *PBFT) handleMessage(msg *ConsensusMessage) {
	switch msg.Type {
	case PrePrepare:
		p.handlePrePrepare(msg)
	case Prepare:
		p.handlePrepare(msg)
	case Commit:
		p.handleCommit(msg)
	case ViewChange:
		p.handleViewChange(msg)
	case NewView:
		// Handle new view message
		if p.state == StateViewChange {
			p.processNewView(p.view)
		}
	}
}

func (p *PBFT) handlePrePrepare(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Verify sequence number and view
	if msg.Sequence < p.sequence {
		return
	}

	// Verify digest
	computedDigest := p.computeDigest(msg.Data)
	if !bytes.Equal(computedDigest, msg.Digest) {
		return
	}

	// Send prepare message
	prepare := &ConsensusMessage{
		Type:     Prepare,
		NodeID:   p.nodeID,
		View:     msg.View,
		Sequence: msg.Sequence,
		Digest:   computedDigest,
		Data:     msg.Data,
	}

	// Store prepare message
	if _, exists := p.prepareMessages[msg.Sequence]; !exists {
		p.prepareMessages[msg.Sequence] = make(map[string]*ConsensusMessage)
	}
	p.prepareMessages[msg.Sequence][p.nodeID] = prepare

	// Broadcast prepare message
	p.broadcast(prepare)
}

func (p *PBFT) handlePrepare(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Initialize prepare messages map for this sequence if needed
	if _, exists := p.prepareMessages[msg.Sequence]; !exists {
		p.prepareMessages[msg.Sequence] = make(map[string]*ConsensusMessage)
	}

	// Store prepare message
	p.prepareMessages[msg.Sequence][msg.NodeID] = msg

	// Check if we have enough prepare messages
	if len(p.prepareMessages[msg.Sequence]) >= 2*p.f {
		// Send commit message
		commit := &ConsensusMessage{
			Type:     Commit,
			NodeID:   p.nodeID,
			View:     msg.View,
			Sequence: msg.Sequence,
			Digest:   msg.Digest,
			Data:     msg.Data,
		}

		// Store commit message
		if _, exists := p.commitMessages[msg.Sequence]; !exists {
			p.commitMessages[msg.Sequence] = make(map[string]*ConsensusMessage)
		}
		p.commitMessages[msg.Sequence][p.nodeID] = commit

		// Broadcast commit message
		p.broadcast(commit)

		// Check if we already have enough commit messages
		if len(p.commitMessages[msg.Sequence]) >= 2*p.f+1 {
			p.executeConsensus(msg.Sequence, msg.Data)
		}
	}
}

func (p *PBFT) handleCommit(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Skip if consensus already reached for this sequence
	if p.consensusReached[msg.Sequence] {
		return
	}

	// Initialize commit messages map for this sequence if needed
	if _, exists := p.commitMessages[msg.Sequence]; !exists {
		p.commitMessages[msg.Sequence] = make(map[string]*ConsensusMessage)
	}

	// Store commit message
	p.commitMessages[msg.Sequence][msg.NodeID] = msg

	// Check if we have enough commit messages
	if len(p.commitMessages[msg.Sequence]) >= 2*p.f+1 {
		// Mark consensus as reached for this sequence
		p.consensusReached[msg.Sequence] = true
		// Consensus reached!
		p.executeConsensus(msg.Sequence, msg.Data)
	}
}

func (p *PBFT) executeConsensus(sequence uint64, data []byte) {
	result := ConsensusResult{
		Sequence: sequence,
		Data:     data,
		Digest:   p.computeDigest(data),
	}

	if p.resultCallback != nil {
		p.resultCallback(result)
	}

	// Create checkpoint after execution
	p.makeCheckpoint()

	// Cleanup old messages
	p.cleanup(sequence)
}

func (p *PBFT) broadcast(msg *ConsensusMessage) error {
	if p.networkManager == nil {
		return fmt.Errorf("network manager not initialized")
	}
	return p.networkManager.Broadcast(msg)
}

func (p *PBFT) validateMessage(msg *ConsensusMessage) error {
	if msg == nil {
		return ErrInvalidMessage
	}

	// Validate sender
	senderValid := false
	for _, node := range p.nodes {
		if node == msg.NodeID {
			senderValid = true
			break
		}
	}
	if !senderValid {
		return ErrInvalidSender
	}

	// Validate sequence number - be more lenient during startup
	if msg.Sequence < p.sequence-1 && p.sequence > 1 {
		return ErrInvalidSequence
	}

	// Validate view number - be more lenient during startup
	if msg.View < p.view-1 && p.view > 1 {
		return ErrInvalidView
	}

	// Validate message type
	switch msg.Type {
	case PrePrepare:
		if !p.isLeader && msg.NodeID == p.nodeID {
			return fmt.Errorf("non-leader node cannot send pre-prepare")
		}
	case Prepare, Commit:
		// These messages can come from any valid node
		break
	case ViewChange, NewView:
		if msg.View <= p.view && p.view > 0 {
			return fmt.Errorf("invalid view number in view change")
		}
	default:
		return fmt.Errorf("unknown message type")
	}

	return nil
}

func (p *PBFT) startViewChange(newView uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if newView <= p.view {
		return
	}

	p.state = StateViewChange

	// Create view change message
	viewChangeData := &ViewChangeData{
		NewView:    newView,
		LastSeq:    p.sequence,
		Checkpoint: p.lastCheckpoint,
	}

	dataBytes, err := json.Marshal(viewChangeData)
	if err != nil {
		log.Printf("Failed to marshal view change data: %v", err)
		return
	}

	msg := &ConsensusMessage{
		Type:     ViewChange,
		NodeID:   p.nodeID,
		Sequence: p.sequence,
		Data:     dataBytes,
	}

	// Initialize view change messages map
	if _, exists := p.viewChangeMsgs[newView]; !exists {
		p.viewChangeMsgs[newView] = make(map[string]*ConsensusMessage)
	}

	p.broadcast(msg)
}

func (p *PBFT) handleViewChange(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var viewChangeData ViewChangeData
	if err := json.Unmarshal(msg.Data, &viewChangeData); err != nil {
		log.Printf("Failed to unmarshal view change data: %v", err)
		return
	}

	// Store view change message
	if _, exists := p.viewChangeMsgs[viewChangeData.NewView]; !exists {
		p.viewChangeMsgs[viewChangeData.NewView] = make(map[string]*ConsensusMessage)
	}
	p.viewChangeMsgs[viewChangeData.NewView][msg.NodeID] = msg

	// Check if we have enough view change messages
	if len(p.viewChangeMsgs[viewChangeData.NewView]) >= 2*p.f+1 {
		p.processNewView(viewChangeData.NewView)
	}
}

func (p *PBFT) processNewView(newView uint64) {
	// Determine new leader
	newLeader := p.nodes[newView%uint64(len(p.nodes))]

	if p.nodeID == newLeader {
		// This node is the new leader
		p.isLeader = true

		// Create new view message
		newViewMsg := &ConsensusMessage{
			Type:     NewView,
			NodeID:   p.nodeID,
			Sequence: p.sequence,
			Data:     []byte{}, // Add any necessary new view data
		}

		p.broadcast(newViewMsg)
	}

	// Update view number
	p.view = newView
	p.state = StateNormal

	// Reset view change messages
	p.viewChangeMsgs = make(map[uint64]map[string]*ConsensusMessage)

	// Reset view timer
	p.resetViewTimer()
}

func (p *PBFT) resetViewTimer() {
	if p.viewTimer != nil {
		p.viewTimer.Stop()
	}
	p.viewTimer = time.AfterFunc(p.viewChangeTimeout, func() {
		p.startViewChange(p.view + 1)
	})
}

func (p *PBFT) computeDigest(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func (p *PBFT) makeCheckpoint() {
	if p.checkpointInterval == 0 {
		p.checkpointInterval = 100
	}

	if p.sequence%p.checkpointInterval == 0 {
		checkpoint := &struct {
			Sequence uint64
			State    []byte
		}{
			Sequence: p.sequence,
			State:    p.lastCheckpoint,
		}

		checkpointBytes, _ := json.Marshal(checkpoint)
		p.checkpoints[p.sequence] = checkpointBytes
		p.lastCheckpointSeq = p.sequence
	}
}

func (p *PBFT) cleanup(sequence uint64) {
	// Cleanup old prepare messages
	for seq := range p.prepareMessages {
		if seq < sequence {
			delete(p.prepareMessages, seq)
		}
	}

	// Cleanup old commit messages
	for seq := range p.commitMessages {
		if seq < sequence {
			delete(p.commitMessages, seq)
		}
	}

	// Cleanup old consensus reached flags
	for seq := range p.consensusReached {
		if seq < sequence {
			delete(p.consensusReached, seq)
		}
	}

	// Cleanup old checkpoints
	for seq := range p.checkpoints {
		if seq < sequence-p.checkpointInterval {
			delete(p.checkpoints, seq)
		}
	}
}

func (p *PBFT) RegisterResultCallback(callback func(ConsensusResult)) {
	p.resultCallback = callback
}

func (p *PBFT) SetNetworkManager(nm *NetworkManager) {
	p.networkManager = nm
}

// IsLeader returns whether this node is the current leader
func (p *PBFT) IsLeader() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.isLeader
}

// Keep the getter methods
func (p *PBFT) GetSequence() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sequence
}

func (p *PBFT) GetView() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.view
}

func (p *PBFT) GetLeader() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nodeID
}

func (p *PBFT) GetState() PBFTState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *PBFT) RestoreState(sequence uint64, view uint64, state PBFTState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sequence = sequence
	p.view = view
	p.state = state
	return nil
}

// SetResultCallback sets the callback function for consensus results
func (p *PBFT) SetResultCallback(callback func(ConsensusResult)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resultCallback = callback
}
