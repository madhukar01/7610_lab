package node

import (
	"time"

	"github.com/mhollas/7610/agent/llm"
	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/similarity"
)

// OracleRequest represents a request to the oracle network
type OracleRequest struct {
	RequestID string                 `json:"request_id"`
	Query     string                 `json:"query"`
	Callback  string                 `json:"callback"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// OracleResponse represents a response from the oracle network
type OracleResponse struct {
	RequestID string                 `json:"request_id"`
	Response  []byte                 `json:"response"`
	Error     error                  `json:"error,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// RequestState represents the current state of an oracle request
type RequestState struct {
	RequestID     string                      `json:"request_id"`
	NodeID        string                      `json:"node_id"`
	Status        string                      `json:"status"`
	Prompt        string                      `json:"prompt"`
	StartTime     time.Time                   `json:"start_time"`
	LLMResponse   *llm.LLMResponse            `json:"llm_response,omitempty"`
	Responses     map[string]*llm.LLMResponse `json:"responses,omitempty"`
	Clusters      []*similarity.Cluster       `json:"clusters,omitempty"`
	ConsensusData *consensus.ConsensusData    `json:"consensus_data,omitempty"`
	IPFSCID       string                      `json:"ipfs_cid,omitempty"`
	Error         error                       `json:"error,omitempty"`
}
