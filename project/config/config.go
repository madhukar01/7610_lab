package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// GlobalConfig represents the complete configuration structure
type GlobalConfig struct {
	Oracle struct {
		Network struct {
			Port      int    `json:"port"`
			Host      string `json:"host"`
			Consensus struct {
				Type      string `json:"type"`
				TimeoutMs int    `json:"timeout_ms"`
				MinNodes  int    `json:"min_nodes"`
				MaxNodes  int    `json:"max_nodes"`
			} `json:"consensus"`
		} `json:"network"`
		Storage struct {
			Provider string `json:"provider"`
			Pinata   struct {
				APIKey    string `json:"api_key"`
				APISecret string `json:"api_secret"`
				JWT       string `json:"jwt"`
			} `json:"pinata"`
		} `json:"storage"`
	} `json:"oracle"`

	LLM struct {
		OpenAI struct {
			APIKey             string  `json:"api_key"`
			Model              string  `json:"model"`
			Temperature        float32 `json:"temperature"`
			MaxTokens          int     `json:"max_tokens"`
			DefaultTemperature float32 `json:"default_temperature"`
			MaxRetries         int     `json:"max_retries"`
			RetryIntervalMs    int     `json:"retry_interval_ms"`
		} `json:"openai"`
		RequestTimeoutMs int `json:"request_timeout_ms"`
		CacheDurationMs  int `json:"cache_duration_ms"`
	} `json:"llm"`

	Blockchain struct {
		Network          string `json:"network"`
		RPCURL           string `json:"rpc_url"`
		ChainID          int    `json:"chain_id"`
		ContractAddress  string `json:"contract_address"`
		OraclePrivateKey string `json:"oracle_private_key"`
		GasLimit         int    `json:"gas_limit"`
		Confirmations    int    `json:"confirmations"`
	} `json:"blockchain"`

	Logging struct {
		Level      string `json:"level"`
		File       string `json:"file"`
		MaxSizeMB  int    `json:"max_size_mb"`
		MaxBackups int    `json:"max_backups"`
		MaxAgeDays int    `json:"max_age_days"`
		Compress   bool   `json:"compress"`
	} `json:"logging"`

	Security struct {
		TLSEnabled        bool   `json:"tls_enabled"`
		CertFile          string `json:"cert_file"`
		KeyFile           string `json:"key_file"`
		AuthTokenExpiryMs int    `json:"auth_token_expiry_ms"`
	} `json:"security"`

	Invoker struct {
		Network         string `json:"network"`
		RPCURL          string `json:"rpc_url"`
		ChainID         int    `json:"chain_id"`
		ContractAddress string `json:"contract_address"`
		PrivateKey      string `json:"private_key"`
		GasLimit        int    `json:"gas_limit"`
		Confirmations   int    `json:"confirmations"`
	} `json:"invoker"`
}

var (
	config     *GlobalConfig
	configOnce sync.Once
	configLock sync.RWMutex
)

// LoadConfig loads the configuration from the specified file
func LoadConfig(configPath string) (*GlobalConfig, error) {
	var err error
	configOnce.Do(func() {
		config = &GlobalConfig{}

		// Read config file
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			err = fmt.Errorf("failed to read config file: %w", readErr)
			return
		}

		// Parse JSON
		if jsonErr := json.Unmarshal(data, config); jsonErr != nil {
			err = fmt.Errorf("failed to parse config file: %w", jsonErr)
			return
		}

		// Load environment variables if specified
		loadEnvVars(config)
	})

	if err != nil {
		return nil, err
	}
	return config, nil
}

// GetConfig returns the current configuration
func GetConfig() *GlobalConfig {
	configLock.RLock()
	defer configLock.RUnlock()
	return config
}

// UpdateConfig updates the configuration and saves it to file
func UpdateConfig(newConfig *GlobalConfig, configPath string) error {
	configLock.Lock()
	defer configLock.Unlock()

	// Create config directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to JSON
	data, err := json.MarshalIndent(newConfig, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	config = newConfig
	return nil
}

// loadEnvVars loads sensitive configuration from environment variables if specified
func loadEnvVars(cfg *GlobalConfig) {
	// Load OpenAI API key from environment if set
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		cfg.LLM.OpenAI.APIKey = apiKey
	}

	// Load Pinata JWT from environment if set
	if jwt := os.Getenv("PINATA_JWT"); jwt != "" {
		cfg.Oracle.Storage.Pinata.JWT = jwt
	}

	// Load RPC URL from environment if set
	if rpcURL := os.Getenv("ETH_RPC_URL"); rpcURL != "" {
		cfg.Blockchain.RPCURL = rpcURL
	}
}
