package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type InvokerConfig struct {
	Network         string `json:"network"`
	RpcUrl          string `json:"rpc_url"`
	ChainId         int    `json:"chain_id"`
	ContractAddress string `json:"contract_address"`
	PrivateKey      string `json:"private_key"`
	GasLimit        int    `json:"gas_limit"`
	Confirmations   int    `json:"confirmations"`
}

type Config struct {
	Invoker InvokerConfig `json:"invoker"`
}

func loadConfig() (*Config, error) {
	data, err := os.ReadFile("../config/config.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	return &config, nil
}

func waitForResponse(client *ethclient.Client, contractABI abi.ABI, contractAddress common.Address, requestID [32]byte) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-ticker.C:
			// Create filter for ResponseReceived events
			query := ethereum.FilterQuery{
				Addresses: []common.Address{contractAddress},
				Topics: [][]common.Hash{{
					contractABI.Events["ResponseReceived"].ID,
					requestID,
				}},
			}

			logs, err := client.FilterLogs(context.Background(), query)
			if err != nil {
				log.Printf("Failed to filter logs: %v", err)
				continue
			}

			for _, vLog := range logs {
				event := struct {
					Response string
					IpfsCid  string
				}{}

				err = contractABI.UnpackIntoInterface(&event, "ResponseReceived", vLog.Data)
				if err != nil {
					log.Printf("Failed to decode event: %v", err)
					continue
				}

				log.Printf("\nResponse received:")
				log.Printf("Response: %s", event.Response)
				log.Printf("IPFS CID: %s", event.IpfsCid)
				return nil
			}

		case <-timeout:
			return fmt.Errorf("timeout waiting for response")
		}
	}
}

func main() {
	// Load config
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to the network
	client, err := ethclient.Dial(cfg.Invoker.RpcUrl)
	if err != nil {
		log.Fatalf("Failed to connect to the network: %v", err)
	}

	// Load private key
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.Invoker.PrivateKey, "0x"))
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatal("Failed to get public key")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	log.Printf("From address: %s", fromAddress.Hex())

	// Get nonce
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		log.Fatalf("Failed to get nonce: %v", err)
	}

	// Get gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Fatalf("Failed to get gas price: %v", err)
	}

	// Load contract ABI
	const abiJSON = `[{"inputs":[{"internalType":"uint256","name":"_requestFee","type":"uint256"},{"internalType":"address","name":"_oracleAddress","type":"address"}],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"node","type":"address"}],"name":"OracleNodeRegistered","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"node","type":"address"}],"name":"OracleNodeRemoved","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"bytes32","name":"requestId","type":"bytes32"},{"indexed":true,"internalType":"address","name":"requester","type":"address"},{"indexed":false,"internalType":"string","name":"prompt","type":"string"}],"name":"RequestCreated","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"bytes32","name":"requestId","type":"bytes32"},{"indexed":false,"internalType":"string","name":"response","type":"string"},{"indexed":false,"internalType":"string","name":"ipfsCid","type":"string"}],"name":"ResponseReceived","type":"event"},{"inputs":[{"internalType":"string","name":"prompt","type":"string"}],"name":"createRequest","outputs":[{"internalType":"bytes32","name":"requestId","type":"bytes32"}],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"getFee","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes32","name":"requestId","type":"bytes32"}],"name":"getRequest","outputs":[{"components":[{"internalType":"address","name":"requester","type":"address"},{"internalType":"string","name":"prompt","type":"string"},{"internalType":"uint256","name":"timestamp","type":"uint256"},{"internalType":"bool","name":"fulfilled","type":"bool"},{"internalType":"string","name":"response","type":"string"},{"internalType":"string","name":"ipfsCid","type":"string"}],"internalType":"struct IOracle.Request","name":"","type":"tuple"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes32","name":"requestId","type":"bytes32"},{"internalType":"string","name":"response","type":"string"},{"internalType":"string","name":"ipfsCid","type":"string"}],"name":"submitResponse","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

	contractABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		log.Fatalf("Failed to parse ABI: %v", err)
	}

	// Prepare transaction data
	prompt := "What is the meaning of life?"
	data, err := contractABI.Pack("createRequest", prompt)
	if err != nil {
		log.Fatalf("Failed to pack data: %v", err)
	}

	// Create transaction
	contractAddress := common.HexToAddress(cfg.Invoker.ContractAddress)
	tx := types.NewTransaction(
		nonce,
		contractAddress,
		big.NewInt(0), // value
		uint64(cfg.Invoker.GasLimit),
		gasPrice,
		data,
	)

	// Get chain ID
	chainID := big.NewInt(int64(cfg.Invoker.ChainId))

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		log.Fatalf("Failed to sign transaction: %v", err)
	}

	// Send transaction
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		log.Fatalf("Failed to send transaction: %v", err)
	}

	log.Printf("Transaction sent: %s", signedTx.Hash().Hex())

	// Wait for transaction receipt
	receipt, err := bind.WaitMined(context.Background(), client, signedTx)
	if err != nil {
		log.Fatalf("Failed to get transaction receipt: %v", err)
	}

	log.Printf("Transaction mined in block %d", receipt.BlockNumber)
	log.Printf("Gas used: %d", receipt.GasUsed)
	log.Printf("Status: %d", receipt.Status)

	var requestID [32]byte
	// Check for events
	if len(receipt.Logs) > 0 {
		log.Printf("Events emitted: %d", len(receipt.Logs))
		for i, vLog := range receipt.Logs {
			log.Printf("Event %d:", i)
			if len(vLog.Topics) > 0 {
				event := struct {
					RequestID common.Hash
					Requester common.Address
					Prompt    string
				}{}
				err := contractABI.UnpackIntoInterface(&event, "RequestCreated", vLog.Data)
				if err != nil {
					log.Printf("Failed to decode event: %v", err)
					continue
				}
				log.Printf("  RequestID: %s", event.RequestID.Hex())
				log.Printf("  Requester: %s", event.Requester.Hex())
				log.Printf("  Prompt: %s", event.Prompt)
				copy(requestID[:], event.RequestID[:])
			}
		}
	} else {
		log.Printf("No events emitted")
	}

	// Wait for oracle response
	log.Printf("\nWaiting for oracle response...")
	err = waitForResponse(client, contractABI, contractAddress, requestID)
	if err != nil {
		log.Printf("Error waiting for response: %v", err)
		return
	}
}