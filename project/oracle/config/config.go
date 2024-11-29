package config

import (
	"time"
)

type Config struct {
	// Network settings
	ListenAddr string
	PeerAddrs  []string

	// Consensus settings
	ConsensusTimeout  time.Duration
	ViewChangeTimeout time.Duration

	// Security settings
	TLSEnabled bool
	CertFile   string
	KeyFile    string
}
