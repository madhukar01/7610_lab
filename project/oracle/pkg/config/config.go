package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	NodeID        string   `json:"node_id"`
	ListenAddress string   `json:"listen_address"`
	Peers         []string `json:"peers"`

	// Consensus settings
	ConsensusTimeout  int `json:"consensus_timeout"`
	ViewChangeTimeout int `json:"view_change_timeout"`

	// Network settings
	MaxMessageSize    int `json:"max_message_size"`
	ConnectionTimeout int `json:"connection_timeout"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
