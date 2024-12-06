package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"agent/src/llm"
	"config"

	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type ContractMetadata struct {
	Output struct {
		Contracts struct {
			ContractFile struct {
				IOracle struct {
					ABI json.RawMessage `json:"abi"`
				} `json:"IOracle"`
			} `json:"contracts/IOracle.sol"`
		} `json:"contracts"`
	} `json:"output"`
}

type OracleListener struct {
	client      *ethclient.Client
	contract    *common.Address
	privateKey  *ecdsa.PrivateKey
	llmClient   *llm.OpenAIClient
	contractABI abi.ABI
	config      *config.GlobalConfig
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
		return abi.ABI{}, fmt.Errorf("failed to parse contract metadata: %w", err)
	}

	// Parse the ABI
	contractABI, err := abi.JSON(strings.NewReader(string(metadata.Output.Contracts.ContractFile.IOracle.ABI)))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse ABI: %w", err)
	}

	return contractABI, nil
}

func NewOracleListener(cfg *config.GlobalConfig) (*OracleListener, error) {
	// Load contract ABI
	contractABI, err := loadContractABI()
	if err != nil {
		return nil, fmt.Errorf("failed to load contract ABI: %w", err)
	}

	// Connect to blockchain
	client, err := ethclient.Dial(cfg.Blockchain.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to blockchain: %v", err)
	}

	// Parse contract address
	contractAddr := common.HexToAddress(cfg.Blockchain.ContractAddress)

	// Parse private key
	privateKey, err := crypto.HexToECDSA(cfg.Blockchain.OraclePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %v", err)
	}

	// Create LLM client
	llmClient, err := llm.NewOpenAIClient(cfg.LLM.OpenAI.APIKey, &llm.Config{
		Model:              cfg.LLM.OpenAI.Model,
		MaxTokens:          cfg.LLM.OpenAI.MaxTokens,
		DefaultTemperature: cfg.LLM.OpenAI.DefaultTemperature,
		MaxRetries:         cfg.LLM.OpenAI.MaxRetries,
		RetryIntervalMs:    cfg.LLM.OpenAI.RetryIntervalMs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM client: %v", err)
	}

	return &OracleListener{
		client:      client,
		contract:    &contractAddr,
		privateKey:  privateKey,
		llmClient:   llmClient,
		contractABI: contractABI,
		config:      cfg,
	}, nil
}

func (o *OracleListener) handleRequest(ctx context.Context, requestID common.Hash, prompt string) error {
	log.Printf("Processing request %s with prompt: %s", requestID.Hex(), prompt)

	// Call LLM
	llmReq := &llm.LLMRequest{
		Prompt:      prompt,
		Temperature: o.config.LLM.OpenAI.DefaultTemperature,
	}

	response, err := o.llmClient.GetResponse(ctx, llmReq)
	if err != nil {
		return fmt.Errorf("failed to get LLM response: %v", err)
	}

	log.Printf("Got LLM response for request %s", requestID.Hex())

	// Submit response to contract
	data, err := o.contractABI.Pack("submitResponse", requestID, response.Text, "ipfs://placeholder")
	if err != nil {
		return fmt.Errorf("failed to pack data: %v", err)
	}

	// Get nonce
	fromAddress := crypto.PubkeyToAddress(o.privateKey.PublicKey)
	nonce, err := o.client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %v", err)
	}

	// Get gas price
	gasPrice, err := o.client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("failed to get gas price: %v", err)
	}

	// Estimate gas
	msg := ethereum.CallMsg{
		From:     fromAddress,
		To:       o.contract,
		Gas:      0,
		GasPrice: gasPrice,
		Value:    big.NewInt(0),
		Data:     data,
	}

	gasLimit, err := o.client.EstimateGas(ctx, msg)
	if err != nil {
		log.Printf("Failed to estimate gas, using default: %v", err)
		gasLimit = uint64(o.config.Blockchain.GasLimit)
	}

	// Add 20% buffer to gas limit
	gasLimit = gasLimit * 120 / 100

	// Create transaction
	tx := types.NewTransaction(
		nonce,
		*o.contract,
		big.NewInt(0),
		gasLimit,
		gasPrice,
		data,
	)

	// Get chain ID
	chainID := big.NewInt(int64(o.config.Blockchain.ChainID))

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), o.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Send transaction
	err = o.client.SendTransaction(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %v", err)
	}

	log.Printf("Submitted response transaction: %s", signedTx.Hash().Hex())

	// Wait for transaction to be mined
	receipt, err := bind.WaitMined(ctx, o.client, signedTx)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction: %v", err)
	}

	if receipt.Status == 0 {
		return fmt.Errorf("transaction failed: %s", signedTx.Hash().Hex())
	}

	log.Printf("Response submitted successfully for request %s", requestID.Hex())
	return nil
}

func (o *OracleListener) Start(ctx context.Context) error {
	log.Printf("Started listening for oracle requests on contract: %s", o.contract.Hex())
	log.Printf("Oracle address: %s", crypto.PubkeyToAddress(o.privateKey.PublicKey).Hex())

	// Create event filter
	query := ethereum.FilterQuery{
		Addresses: []common.Address{*o.contract},
		Topics: [][]common.Hash{{
			o.contractABI.Events["RequestCreated"].ID,
		}},
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	lastBlock := uint64(0)
	log.Printf("Starting block scanner...")

	for {
		select {
		case <-ticker.C:
			// Get latest block
			latestBlock, err := o.client.BlockNumber(ctx)
			if err != nil {
				log.Printf("Failed to get latest block: %v", err)
				continue
			}

			if lastBlock == 0 {
				log.Printf("Initializing scanner at block %d", latestBlock)
				lastBlock = latestBlock
				continue
			}

			// Update query with new block range
			query.FromBlock = new(big.Int).SetUint64(lastBlock + 1)
			query.ToBlock = new(big.Int).SetUint64(latestBlock)

			blockRange := latestBlock - lastBlock
			log.Printf("Scanning blocks %d to %d (%d blocks)...", lastBlock+1, latestBlock, blockRange)

			// Get logs
			logs, err := o.client.FilterLogs(ctx, query)
			if err != nil {
				log.Printf("Failed to filter logs: %v", err)
				continue
			}

			if len(logs) > 0 {
				log.Printf("Found %d events in blocks %d to %d", len(logs), lastBlock+1, latestBlock)
			}

			// Process logs
			for _, vLog := range logs {
				if len(vLog.Topics) < 3 {
					continue
				}

				requestID := vLog.Topics[1]
				event := struct {
					Prompt string
				}{}

				err := o.contractABI.UnpackIntoInterface(&event, "RequestCreated", vLog.Data)
				if err != nil {
					log.Printf("Failed to decode event: %v", err)
					continue
				}

				log.Printf("Found request in block %d:", vLog.BlockNumber)
				log.Printf("  Transaction: %s", vLog.TxHash.Hex())
				log.Printf("  Request ID: %s", requestID.Hex())
				log.Printf("  Prompt: %s", event.Prompt)

				go func(id common.Hash, p string) {
					if err := o.handleRequest(ctx, id, p); err != nil {
						log.Printf("Failed to handle request: %v", err)
					}
				}(requestID, event.Prompt)
			}

			lastBlock = latestBlock

		case <-ctx.Done():
			log.Printf("Shutting down block scanner...")
			return nil
		}
	}
}

func main() {
	cfg, err := config.LoadConfig("../config/config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	listener, err := NewOracleListener(cfg)
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}

	ctx := context.Background()
	if err := listener.Start(ctx); err != nil {
		log.Fatalf("Listener error: %v", err)
	}
}
