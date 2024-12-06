package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mhollas/7610/agent/llm"
	"github.com/mhollas/7610/config"
	"github.com/mhollas/7610/oracle/pkg/consensus"
	"github.com/mhollas/7610/oracle/pkg/logging"
	"github.com/mhollas/7610/oracle/pkg/network"
	oraclenode "github.com/mhollas/7610/oracle/pkg/node"
	"github.com/mhollas/7610/oracle/pkg/similarity"
	"github.com/mhollas/7610/oracle/pkg/storage"
	oracletypes "github.com/mhollas/7610/oracle/pkg/types"
)

func main() {
	// Parse command line flags
	nodeID := flag.Int("id", 0, "Node ID (0-3)")
	basePort := flag.Int("port", 3000, "Base port number")
	flag.Parse()

	if *nodeID < 0 || *nodeID > 3 {
		fmt.Printf("Node ID must be between 0 and 3\n")
		os.Exit(1)
	}

	// Create logger
	logger := logging.NewLogger(logging.DEBUG, os.Stdout, fmt.Sprintf("node-%d", *nodeID))
	logger.Info("Starting Oracle Node", map[string]interface{}{"id": *nodeID})

	// Load config
	cfg, err := config.LoadConfig("../config/config.json")
	if err != nil {
		logger.Error("Failed to load config", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	// Create LLM client
	logger.Info("Initializing LLM client", map[string]interface{}{"node": *nodeID})
	llmConfig := &llm.Config{
		Model:              cfg.LLM.OpenAI.Model,
		MaxTokens:          cfg.LLM.OpenAI.MaxTokens,
		DefaultTemperature: cfg.LLM.OpenAI.DefaultTemperature,
		MaxRetries:         cfg.LLM.OpenAI.MaxRetries,
		RetryIntervalMs:    cfg.LLM.OpenAI.RetryIntervalMs,
	}
	llmClient, err := llm.NewOpenAIClient(cfg.LLM.OpenAI.APIKey, llmConfig)
	if err != nil {
		logger.Error("Failed to create LLM client", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	// Create storage client
	logger.Info("Initializing storage client", map[string]interface{}{"node": *nodeID})
	storageClient, err := storage.NewPinataClient(
		cfg.Oracle.Storage.Pinata.APIKey,
		cfg.Oracle.Storage.Pinata.APISecret,
		cfg.Oracle.Storage.Pinata.JWT,
	)
	if err != nil {
		logger.Error("Failed to create storage client", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	// Create semantic scorer
	logger.Info("Initializing semantic scorer", map[string]interface{}{"node": *nodeID})
	scorer := similarity.NewSemanticScorer(cfg.LLM.OpenAI.APIKey, "")

	// Create network transport
	nodeAddr := fmt.Sprintf("127.0.0.1:%d", *basePort+*nodeID)
	nodeIDStr := fmt.Sprintf("node%d", *nodeID)
	transport := network.NewP2PTransport(nodeIDStr, nodeAddr)

	// Create node
	logger.Info("Creating oracle node", map[string]interface{}{
		"id":      *nodeID,
		"address": nodeAddr,
	})
	oracleNode, err := oraclenode.NewOracleNode(nodeIDStr, nodeAddr, llmClient, storageClient, scorer, logger)
	if err != nil {
		logger.Error("Failed to create node", map[string]interface{}{"error": err.Error()})
		os.Exit(1)
	}

	// Initialize consensus with transport
	peers := make([]string, 0)
	peerAddrs := make(map[string]string)

	// Create peer list and address mapping
	for i := 0; i < 4; i++ {
		if i != *nodeID {
			peerID := fmt.Sprintf("node%d", i)
			peerAddr := fmt.Sprintf("127.0.0.1:%d", *basePort+i)
			peers = append(peers, peerID)
			peerAddrs[peerID] = peerAddr
		}
	}

	logger.Info("Initializing consensus", map[string]interface{}{
		"node":      *nodeID,
		"peers":     peers,
		"peerAddrs": peerAddrs,
	})

	// Initialize consensus and register peers
	oracleNode.InitConsensus(peers)
	oracleNode.SetTransport(transport)

	// Register peer addresses
	for peerID, peerAddr := range peerAddrs {
		logger.Info("Registering peer", map[string]interface{}{
			"node":    *nodeID,
			"peer":    peerID,
			"address": peerAddr,
		})
		if err := oracleNode.RegisterPeer(peerID, peerAddr); err != nil {
			logger.Error("Failed to register peer", map[string]interface{}{
				"error": err.Error(),
				"peer":  peerID,
			})
			os.Exit(1)
		}
	}

	// Message tracking
	var msgMutex sync.RWMutex
	msgCounts := make(map[consensus.MessageType]int)

	// Add message tracking callback
	origCallback := oracleNode.GetMessageCallback()
	oracleNode.SetMessageCallback(func(msg *consensus.ConsensusMessage) {
		// Call original callback
		if origCallback != nil {
			origCallback(msg)
		}

		// Track message
		msgMutex.Lock()
		msgCounts[msg.Type]++
		msgMutex.Unlock()

		// Get message type string
		var typeStr string
		switch msg.Type {
		case consensus.PrePrepare:
			typeStr = "PrePrepare"
		case consensus.Prepare:
			typeStr = "Prepare"
		case consensus.Commit:
			typeStr = "Commit"
		case consensus.ViewChange:
			typeStr = "ViewChange"
		case consensus.NewView:
			typeStr = "NewView"
		default:
			typeStr = "Unknown"
		}

		// Try to extract request ID from consensus data
		var requestID string
		if msg.Data != nil {
			var consensusData consensus.ConsensusData
			if err := json.Unmarshal(msg.Data, &consensusData); err == nil {
				requestID = consensusData.RequestID
			}
		}

		// Log message details
		fields := map[string]interface{}{
			"node":     *nodeID,
			"type":     typeStr,
			"sequence": msg.Sequence,
			"view":     msg.View,
			"from":     msg.NodeID,
		}
		if requestID != "" {
			fields["requestID"] = requestID
		}

		logger.Debug("Received consensus message", fields)

		// Log consensus progress
		if msg.Type == consensus.Commit {
			msgMutex.RLock()
			prepareCount := msgCounts[consensus.Prepare]
			commitCount := msgCounts[consensus.Commit]
			msgMutex.RUnlock()

			logger.Debug("Consensus progress", map[string]interface{}{
				"node":         *nodeID,
				"requestID":    requestID,
				"prepareCount": prepareCount,
				"commitCount":  commitCount,
				"threshold":    2*len(peers)/3 + 1,
			})
		}
	})

	// Register consensus result callback
	oracleNode.GetConsensus().SetResultCallback(func(result consensus.ConsensusResult) {
		logger.Info("Consensus reached", map[string]interface{}{
			"node":     *nodeID,
			"sequence": result.Sequence,
		})

		var consensusData consensus.ConsensusData
		if err := json.Unmarshal(result.Data, &consensusData); err != nil {
			logger.Error("Failed to unmarshal consensus data", map[string]interface{}{
				"error": err.Error(),
			})
			return
		}

		logger.Info("Consensus result", map[string]interface{}{
			"node":      *nodeID,
			"requestID": consensusData.RequestID,
			"response":  consensusData.Response.Text,
		})
	})

	// Create HTTP server for request handling
	if *nodeID == 0 { // Only leader node handles HTTP requests
		http.HandleFunc("/request", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			// Parse request
			var req oracletypes.OracleRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				logger.Error("Failed to decode request", map[string]interface{}{"error": err.Error()})
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			logger.Info("Received oracle request", map[string]interface{}{
				"requestID": req.RequestID,
				"prompt":    req.Prompt,
			})

			// Process request
			if err := oracleNode.ProcessPrompt(r.Context(), req.RequestID, req.Prompt); err != nil {
				logger.Error("Failed to process request", map[string]interface{}{
					"error":     err.Error(),
					"requestID": req.RequestID,
				})
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Wait for consensus
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			timeout := time.After(30 * time.Second)

			for {
				select {
				case <-ticker.C:
					state, err := oracleNode.GetRequestState(req.RequestID)
					if err != nil {
						continue
					}

					logger.Debug("Request state", map[string]interface{}{
						"requestID": req.RequestID,
						"status":    state.Status,
						"error":     state.Error,
					})

					if state.Status == "complete" {
						resp := oracletypes.OracleResponse{
							RequestID: req.RequestID,
							Response:  state.ConsensusData.Response.Text,
						}
						json.NewEncoder(w).Encode(resp)
						return
					} else if state.Status == "failed" {
						var errMsg string
						if state.Error != nil {
							errMsg = state.Error.Error()
						} else {
							errMsg = "Unknown error"
						}
						resp := oracletypes.OracleResponse{
							RequestID: req.RequestID,
							Error:     errMsg,
						}
						json.NewEncoder(w).Encode(resp)
						return
					}

				case <-timeout:
					logger.Error("Request timed out", map[string]interface{}{"requestID": req.RequestID})
					http.Error(w, "Request timed out", http.StatusGatewayTimeout)
					return

				case <-r.Context().Done():
					logger.Error("Request cancelled", map[string]interface{}{"requestID": req.RequestID})
					http.Error(w, "Request cancelled", http.StatusGatewayTimeout)
					return
				}
			}
		})

		// Start HTTP server
		httpAddr := fmt.Sprintf(":%d", *basePort)
		logger.Info("Starting HTTP server", map[string]interface{}{"address": httpAddr})
		go func() {
			if err := http.ListenAndServe(httpAddr, nil); err != nil {
				logger.Error("HTTP server error", map[string]interface{}{"error": err.Error()})
				os.Exit(1)
			}
		}()
	}

	// Start node
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger.Info("Starting node", map[string]interface{}{"id": *nodeID})
	go func() {
		if err := oracleNode.Start(ctx); err != nil {
			logger.Error("Node error", map[string]interface{}{"error": err.Error()})
			cancel()
		}
	}()

	logger.Info("Node status", map[string]interface{}{
		"id":        *nodeID,
		"address":   nodeAddr,
		"role":      map[bool]string{true: "Leader", false: "Follower"}[*nodeID == 0],
		"peers":     peers,
		"peerAddrs": peerAddrs,
	})

	// Start periodic status reporting
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				msgMutex.Lock()
				stats := make(map[string]interface{})
				for msgType, count := range msgCounts {
					var typeStr string
					switch msgType {
					case consensus.PrePrepare:
						typeStr = "PrePrepare"
					case consensus.Prepare:
						typeStr = "Prepare"
					case consensus.Commit:
						typeStr = "Commit"
					case consensus.ViewChange:
						typeStr = "ViewChange"
					case consensus.NewView:
						typeStr = "NewView"
					default:
						typeStr = "Unknown"
					}
					stats[typeStr] = count
				}
				logger.Info("Message statistics", map[string]interface{}{
					"node":  *nodeID,
					"stats": stats,
				})
				msgMutex.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down node", map[string]interface{}{"id": *nodeID})
	ticker.Stop()
	cancel()
}
