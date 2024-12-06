package storage

import (
	"context"
	"time"
)

// StorageClient defines the interface for IPFS storage operations
type StorageClient interface {
	StoreExecutionRecord(ctx context.Context, record *ExecutionRecord) (string, error)
	GetExecutionRecord(ctx context.Context, cid string) (*ExecutionRecord, error)
}

// OracleResponse represents a single oracle node's response
type OracleResponse struct {
	NodeID      string                 `json:"node_id"`
	LLMResponse string                 `json:"llm_response"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// ConsensusData represents the consensus process and result
type ConsensusData struct {
	Method           string  `json:"method"` // e.g., "PBFT", "Semantic Clustering"
	ParticipantCount int     `json:"participant_count"`
	AgreementScore   float32 `json:"agreement_score"` // e.g., semantic similarity score
	Round            int     `json:"round"`
}

// ExecutionRecord represents the complete record of one smart contract execution
type ExecutionRecord struct {
	// Request Details
	RequestID    string    `json:"request_id"`
	RequestInput string    `json:"request_input"`
	Timestamp    time.Time `json:"timestamp"`

	// Oracle Responses
	OracleResponses []OracleResponse `json:"oracle_responses"`

	// Consensus Information
	Consensus ConsensusData `json:"consensus"`

	// Final Result
	FinalResponse string `json:"final_response"`

	// Optional Contract Details
	ContractAddress string `json:"contract_address,omitempty"`
	TransactionHash string `json:"transaction_hash,omitempty"`
	BlockNumber     uint64 `json:"block_number,omitempty"`
	NetworkID       string `json:"network_id,omitempty"`
	GasUsed         uint64 `json:"gas_used,omitempty"`

	// Additional Metadata
	ExtraMetadata map[string]interface{} `json:"extra_metadata,omitempty"`
}
