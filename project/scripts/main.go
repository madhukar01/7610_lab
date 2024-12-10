package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/mhollas/7610/config"
	"github.com/mhollas/7610/oracle/pkg/logging"
)

// OracleRequest represents a request to the oracle network
type OracleRequest struct {
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
}

// OracleResponse represents a response from the oracle network
type OracleResponse struct {
	IPFSCID string `json:"ipfs_cid"`
	Error   string `json:"error,omitempty"`
}

// ContractMetadata represents the metadata for the contract
type ContractMetadata struct {
	ID              string `json:"id"`
	Format          string `json:"_format"`
	SolcVersion     string `json:"solcVersion"`
	SolcLongVersion string `json:"solcLongVersion"`
	Input           struct {
		Language string `json:"language"`
		Sources  map[string]struct {
			Content string `json:"content"`
		} `json:"sources"`
		Settings struct {
			Optimizer struct {
				Enabled bool `json:"enabled"`
				Runs    int  `json:"runs"`
			} `json:"optimizer"`
			OutputSelection map[string]map[string][]string `json:"outputSelection"`
		} `json:"settings"`
	} `json:"input"`
	Output struct {
		Contracts map[string]map[string]struct {
			ABI json.RawMessage `json:"abi"`
		} `json:"contracts"`
	} `json:"output"`
}

func loadContractABI() (abi.ABI, error) {
	// Read the contract metadata file
	data, err := os.ReadFile("../contracts/contract_metadata.json")
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to read contract metadata: %w", err)
	}

	// Parse the metadata
	var metadata ContractMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Get the ABI from the IOracle contract
	contracts, ok := metadata.Output.Contracts["contracts/IOracle.sol"]
	if !ok {
		return abi.ABI{}, fmt.Errorf("IOracle contract not found in metadata")
	}

	ioracle, ok := contracts["IOracle"]
	if !ok {
		return abi.ABI{}, fmt.Errorf("IOracle interface not found in contract")
	}

	// Parse the ABI
	contractABI, err := abi.JSON(strings.NewReader(string(ioracle.ABI)))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse ABI: %w", err)
	}

	return contractABI, nil
}

func sendToOracle(ctx context.Context, logger *logging.Logger, requestID string, prompt string) (string, error) {
	// Create request
	req := OracleRequest{
		RequestID: requestID,
		Prompt:    prompt,
	}

	// Marshal request
	reqData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send to oracle leader (node0)
	url := "http://localhost:8080/prompt"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(reqData)))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Make request with no timeout
	client := &http.Client{
		Timeout: 0, // No timeout
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oracle returned error status: %d", resp.StatusCode)
	}

	// Parse response
	var oracleResp OracleResponse
	if err := json.NewDecoder(resp.Body).Decode(&oracleResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for oracle error
	if oracleResp.Error != "" {
		return "", fmt.Errorf("oracle error: %s", oracleResp.Error)
	}

	// Log the IPFS CID
	logger.Info(fmt.Sprintf("\n=== Oracle Response ===\nRequest ID: %s\nIPFS CID: %s\n====================\n",
		requestID, oracleResp.IPFSCID))

	return oracleResp.IPFSCID, nil
}

func monitorBlockchain(ctx context.Context, cfg *config.GlobalConfig, logger *logging.Logger, client *ethclient.Client, contractABI abi.ABI, privateKey string) {
	contractAddr := common.HexToAddress(cfg.Blockchain.ContractAddress)
	logger.Info(fmt.Sprintf("Starting blockchain monitor on contract %s (%s)",
		contractAddr.Hex(), cfg.Blockchain.Network))

	// Create event filter
	query := ethereum.FilterQuery{
		Addresses: []common.Address{contractAddr},
		Topics: [][]common.Hash{{
			contractABI.Events["RequestCreated"].ID,
		}},
	}

	// Track last checked block
	lastBlock := uint64(0)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentBlock, err := client.BlockNumber(ctx)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to get current block: %v", err))
				continue
			}

			if lastBlock == 0 {
				lastBlock = currentBlock - 100 // Start from 100 blocks ago
				logger.Info(fmt.Sprintf("Starting block scan from %d to %d (range: %d blocks)",
					lastBlock, currentBlock, currentBlock-lastBlock))
			}

			// Update query with new block range
			query.FromBlock = new(big.Int).SetUint64(lastBlock + 1)
			query.ToBlock = new(big.Int).SetUint64(currentBlock)

			// Get logs
			logs, err := client.FilterLogs(ctx, query)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to filter logs: %v", err))
				continue
			}

			// Log block check every 10 blocks
			blockRange := currentBlock - lastBlock
			logger.Info(fmt.Sprintf("Checked blocks %d to %d (range: %d blocks, events: %d)",
				lastBlock+1, currentBlock, blockRange, len(logs)))

			// Process logs
			for _, vLog := range logs {
				if len(vLog.Topics) < 3 {
					continue
				}

				requestID := vLog.Topics[1]
				event := struct {
					Prompt string
				}{}

				err := contractABI.UnpackIntoInterface(&event, "RequestCreated", vLog.Data)
				if err != nil {
					logger.Error(fmt.Sprintf("Failed to decode event: %v", err))
					continue
				}

				logger.Info(fmt.Sprintf("Found request %s in block %d (tx: %s): %s",
					requestID.Hex(), vLog.BlockNumber, vLog.TxHash.Hex(), event.Prompt))

				// Send request to oracle and wait for response
				response, err := sendToOracle(ctx, logger, requestID.Hex(), event.Prompt)
				if err != nil {
					logger.Error(fmt.Sprintf("Failed to get oracle response: %v", err))
					continue
				}

				logger.Info(fmt.Sprintf("Got oracle response for request %s: %s",
					requestID.Hex(), response))

				// Submit response back to blockchain
				auth, err := createTransactionAuth(ctx, client, privateKey)
				if err != nil {
					logger.Error(fmt.Sprintf("Failed to create transaction auth: %v", err))
					continue
				}

				// Create contract instance
				contract := bind.NewBoundContract(contractAddr, contractABI, client, client, client)

				// Submit response
				tx, err := contract.Transact(auth, "submitResponse", requestID, response, response)
				if err != nil {
					logger.Error(fmt.Sprintf("Failed to submit response: %v", err))
					continue
				}

				logger.Info(fmt.Sprintf("Submitted response for request %s (tx: %s)",
					requestID.Hex(), tx.Hash().Hex()))
			}

			lastBlock = currentBlock
		}
	}
}

func createTransactionAuth(ctx context.Context, client *ethclient.Client, privateKeyHex string) (*bind.TransactOpts, error) {
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, auth.From)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0)
	auth.GasLimit = uint64(300000)
	auth.GasPrice = gasPrice

	return auth, nil
}

func runListener() error {
	// Load config
	cfg, err := config.LoadConfig("../config/config.json")
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Create logger
	logger := logging.NewLogger(logging.INFO, os.Stdout, "listener")
	logger.Info("Starting oracle listener...")

	// Load contract ABI
	contractABI, err := loadContractABI()
	if err != nil {
		return fmt.Errorf("failed to load contract ABI: %v", err)
	}
	logger.Info("Loaded contract ABI")

	// Connect to blockchain
	client, err := ethclient.Dial(cfg.Blockchain.RPCURL)
	if err != nil {
		return fmt.Errorf("failed to connect to blockchain: %v", err)
	}
	logger.Info(fmt.Sprintf("Connected to blockchain at %s (%s)",
		cfg.Blockchain.RPCURL, cfg.Blockchain.Network))

	// Start blockchain monitor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go monitorBlockchain(ctx, cfg, logger, client, contractABI, cfg.Blockchain.OraclePrivateKey)

	logger.Info(fmt.Sprintf("Oracle listener fully started on contract %s (%s)",
		cfg.Blockchain.ContractAddress, cfg.Blockchain.Network))

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down listener...")
	cancel()
	return nil
}

func main() {
	if err := runListener(); err != nil {
		log.Fatalf("Error running listener: %v", err)
	}
}
