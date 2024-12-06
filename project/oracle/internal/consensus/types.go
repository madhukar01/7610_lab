package consensus

import (
	"agent/src/llm"
	"crypto/ecdsa"
	"sync"
	"time"
)

// MessageType represents the type of consensus message
type MessageType int

const (
	PrePrepare MessageType = iota
	Prepare
	Commit
	ViewChange
	NewView
	StateTransfer
)

// ConsensusMessage represents a message in the consensus protocol
type ConsensusMessage struct {
	Type     MessageType `json:"type"`
	NodeID   string      `json:"node_id"`
	View     uint64      `json:"view"`
	Sequence uint64      `json:"sequence"`
	Digest   []byte      `json:"digest"`
	Data     []byte      `json:"data"`
}

// ConsensusData represents the data being agreed upon
type ConsensusData struct {
	RequestID string           `json:"request_id"`
	NodeID    string           `json:"node_id"`
	Response  *llm.LLMResponse `json:"response"`
	Timestamp time.Time        `json:"timestamp"`
}

// ConsensusResult represents the result of consensus
type ConsensusResult struct {
	Sequence uint64 `json:"sequence"`
	Data     []byte `json:"data"`
	Digest   []byte `json:"digest"`
}

// NodeState represents the state of a PBFT node
type NodeState int

const (
	StateNormal NodeState = iota
	StateViewChange
)

// ViewChangeMessage represents a view change message
type ViewChangeMessage struct {
	NodeID       string            `json:"node_id"`
	NewView      uint64            `json:"new_view"`
	LastSequence uint64            `json:"last_sequence"`
	Checkpoints  map[uint64][]byte `json:"checkpoints"`
	PrepareProof map[uint64][]byte `json:"prepare_proof"`
}

// NewViewMessage represents a new view message
type NewViewMessage struct {
	NodeID      string              `json:"node_id"`
	View        uint64              `json:"view"`
	ViewChanges []ViewChangeMessage `json:"view_changes"`
	NewSequence uint64              `json:"new_sequence"`
}

// ViewChangeData contains data for view change messages
type ViewChangeData struct {
	NewView    uint64 `json:"new_view"`
	LastSeq    uint64 `json:"last_seq"`
	Checkpoint []byte `json:"checkpoint"`
}

// CheckpointProof represents a proof of a checkpoint
type CheckpointProof struct {
	Sequence   uint64            `json:"sequence"`
	Digest     []byte            `json:"digest"`
	State      []byte            `json:"state"`
	Signatures map[string][]byte `json:"signatures"`
}

// PBFT represents a PBFT consensus instance
type PBFT struct {
	mu                 sync.RWMutex
	nodeID             string
	nodes              []string
	privateKey         *ecdsa.PrivateKey
	networkManager     *NetworkManager
	timeout            time.Duration
	viewChangeTimeout  time.Duration
	checkpointInterval uint64

	// State
	view              uint64
	sequence          uint64
	state             NodeState
	isLeader          bool
	lastCheckpoint    []byte
	lastCheckpointSeq uint64

	// Messages
	prepareMessages map[uint64]map[string]*ConsensusMessage
	commitMessages  map[uint64]map[string]*ConsensusMessage
	viewChangeMsgs  map[uint64]map[string]*ConsensusMessage
	checkpoints     map[uint64][]byte

	// Channels
	msgChan  chan *ConsensusMessage
	doneChan chan struct{}

	// Callbacks
	resultCallback func(ConsensusResult)

	// Configuration
	f         int // Byzantine fault tolerance (n-1)/3
	viewTimer *time.Timer
}
