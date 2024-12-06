package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadAndUpdate(t *testing.T) {
	// Create a temporary directory for test config
	tempDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.json")

	// Create test config
	testConfig := &GlobalConfig{}
	testConfig.Oracle.Network.Port = 8545
	testConfig.Oracle.Network.Host = "localhost"
	testConfig.Oracle.Network.Consensus.Type = "pbft"
	testConfig.LLM.OpenAI.Model = "gpt-4-1106-preview"
	testConfig.Blockchain.Network = "sepolia"

	// Test UpdateConfig
	if err := UpdateConfig(testConfig, configPath); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Test LoadConfig
	loadedConfig, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify loaded config
	if loadedConfig.Oracle.Network.Port != testConfig.Oracle.Network.Port {
		t.Errorf("Expected port %d, got %d", testConfig.Oracle.Network.Port, loadedConfig.Oracle.Network.Port)
	}
	if loadedConfig.LLM.OpenAI.Model != testConfig.LLM.OpenAI.Model {
		t.Errorf("Expected model %s, got %s", testConfig.LLM.OpenAI.Model, loadedConfig.LLM.OpenAI.Model)
	}
}

func TestEnvironmentVariables(t *testing.T) {
	// Set test environment variables
	os.Setenv("OPENAI_API_KEY", "test-api-key")
	os.Setenv("WEB3_STORAGE_TOKEN", "test-storage-token")
	os.Setenv("ETH_RPC_URL", "test-rpc-url")
	defer func() {
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("WEB3_STORAGE_TOKEN")
		os.Unsetenv("ETH_RPC_URL")
	}()

	// Create a temporary config file
	tempDir, err := os.MkdirTemp("", "config-env-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.json")

	// Create and save initial config
	testConfig := &GlobalConfig{}
	if err := UpdateConfig(testConfig, configPath); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Load config and check environment variables
	loadedConfig, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify environment variables were loaded
	if loadedConfig.LLM.OpenAI.APIKey != "test-api-key" {
		t.Error("OpenAI API key not loaded from environment")
	}
	if loadedConfig.Oracle.Storage.Web3StorageToken != "test-storage-token" {
		t.Error("Web3.Storage token not loaded from environment")
	}
	if loadedConfig.Blockchain.RPCURL != "test-rpc-url" {
		t.Error("RPC URL not loaded from environment")
	}
}

func TestGetConfig(t *testing.T) {
	// Create a temporary config file
	tempDir, err := os.MkdirTemp("", "config-get-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.json")

	// Create test config
	testConfig := &GlobalConfig{}
	testConfig.Oracle.Network.Port = 8545

	// Update config
	if err := UpdateConfig(testConfig, configPath); err != nil {
		t.Fatalf("Failed to update config: %v", err)
	}

	// Load config
	_, err = LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test GetConfig
	cfg := GetConfig()
	if cfg == nil {
		t.Fatal("GetConfig returned nil")
	}
	if cfg.Oracle.Network.Port != testConfig.Oracle.Network.Port {
		t.Errorf("Expected port %d, got %d", testConfig.Oracle.Network.Port, cfg.Oracle.Network.Port)
	}
}
