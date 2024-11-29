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
	f := (len(nodes) - 1) / 3
	pbft := &PBFT{
		nodeID:            nodeID,
		nodes:             nodes,
		f:                 f,
		timeout:           timeout,
		prepareMessages:   make(map[uint64]map[string]*ConsensusMessage),
		commitMessages:    make(map[uint64]map[string]*ConsensusMessage),
		msgChan:           make(chan *ConsensusMessage, 1000),
		doneChan:          make(chan struct{}),
		viewChangeTimeout: timeout * 2, // View change timeout is typically longer than normal timeout
		viewChangeMsgs:    make(map[uint64]map[string]*ConsensusMessage),
		state:             Normal,
		checkpoints:       make(map[uint64][]byte),
		networkManager:    nil,
	}
	pbft.resetViewTimer()
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

	digest := p.computeDigest(value)
	msg := &ConsensusMessage{
		Type:     PrePrepare,
		NodeID:   p.nodeID,
		View:     p.view,
		Sequence: p.sequence,
		Digest:   digest,
		Data:     value,
	}

	p.broadcast(msg)
	p.sequence++
	return nil
}

func (p *PBFT) ProcessMessage(msg *ConsensusMessage) error {
	if err := p.validateMessage(msg); err != nil {
		return fmt.Errorf("message validation failed: %w", err)
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
		if p.state == ViewChangeState {
			p.processNewView(p.view)
		}
	}
}

func (p *PBFT) handlePrePrepare(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Verify sequence number and view
	if msg.Sequence != p.sequence {
		return
	}

	// Send prepare message
	prepare := &ConsensusMessage{
		Type:     Prepare,
		NodeID:   p.nodeID,
		Sequence: msg.Sequence,
		Data:     msg.Data,
	}

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
			Sequence: msg.Sequence,
			Data:     msg.Data,
		}
		p.broadcast(commit)
	}
}

func (p *PBFT) handleCommit(msg *ConsensusMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Initialize commit messages map for this sequence if needed
	if _, exists := p.commitMessages[msg.Sequence]; !exists {
		p.commitMessages[msg.Sequence] = make(map[string]*ConsensusMessage)
	}

	// Store commit message
	p.commitMessages[msg.Sequence][msg.NodeID] = msg

	// Check if we have enough commit messages
	if len(p.commitMessages[msg.Sequence]) >= 2*p.f+1 {
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

func (p *PBFT) broadcast(msg *ConsensusMessage) {
	if p.networkManager != nil {
		if err := p.networkManager.Broadcast(msg); err != nil {
			log.Printf("Failed to broadcast message: %v", err)
		}
	}
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

	// Validate view number
	if msg.View < p.view {
		return ErrInvalidView
	}

	// Validate sequence number
	if msg.Sequence < p.sequence {
		return ErrInvalidSequence
	}

	// Validate digest
	computedDigest := p.computeDigest(msg.Data)
	if !bytes.Equal(computedDigest, msg.Digest) {
		return fmt.Errorf("invalid message digest")
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
		if msg.View <= p.view {
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

	p.state = ViewChangeState

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
	p.state = Normal

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
