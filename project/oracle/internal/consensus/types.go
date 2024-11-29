package consensus

import (
	"context"
	"sync"
	"time"
)

// ConsensusMessage represents a message in the PBFT consensus
type ConsensusMessage struct {
	Type     MessageType
	NodeID   string
	View     uint64
	Sequence uint64
	Digest   []byte
	Data     []byte
}

const (
	// PBFT States
	Normal PBFTState = iota
	ViewChangeState
)

const (
	// Message Types
	PrePrepare MessageType = iota
	Prepare
	Commit
	ViewChange
	NewView
)

type MessageType int
type PBFTState int

type ViewChangeData struct {
	NewView    uint64
	LastSeq    uint64
	Checkpoint []byte
}

// ConsensusResult represents the result of consensus
type ConsensusResult struct {
	Sequence uint64
	Data     []byte
	Digest   []byte
}

// ConsensusEngine defines the interface for consensus operations
type ConsensusEngine interface {
	// Start starts the consensus engine
	Start(ctx context.Context) error

	// Stop stops the consensus engine
	Stop() error

	// ProcessMessage processes an incoming consensus message
	ProcessMessage(msg *ConsensusMessage) error

	// ProposeValue proposes a new value for consensus
	ProposeValue(value []byte) error

	// RegisterResultCallback registers a callback for consensus results
	RegisterResultCallback(func(ConsensusResult))
}

// PBFT represents the PBFT consensus engine
type PBFT struct {
	mu       sync.RWMutex
	nodeID   string
	view     uint64 // Current view number
	sequence uint64 // Sequence number for consensus
	state    PBFTState
	isLeader bool

	// Consensus state
	prepareMessages map[uint64]map[string]*ConsensusMessage
	commitMessages  map[uint64]map[string]*ConsensusMessage

	// Channels for message handling
	msgChan  chan *ConsensusMessage
	doneChan chan struct{}

	// Configuration
	nodes   []string // List of node IDs
	f       int      // Byzantine fault tolerance (n-1)/3
	timeout time.Duration

	viewChangeTimeout  time.Duration
	viewChangeMsgs     map[uint64]map[string]*ConsensusMessage
	lastCheckpoint     []byte
	viewTimer          *time.Timer
	checkpointInterval uint64
	lastCheckpointSeq  uint64
	checkpoints        map[uint64][]byte
	resultCallback     func(ConsensusResult)
	networkManager     *NetworkManager
}
