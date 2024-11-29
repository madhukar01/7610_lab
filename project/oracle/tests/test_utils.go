package tests

import (
	"fmt"
	"net"
	"oracle/internal/logging"
	"oracle/internal/node"
)

// CreateTestNode creates a node for testing
func CreateTestNode(nodeID string, peers []string) (*node.OracleNode, error) {
	logger := logging.NewLogger(logging.DEBUG, nil, "test")
	addr := fmt.Sprintf("localhost:%s", GetFreePort())

	// Fix: Add logger parameter to NewOracleNode call
	n, err := node.NewOracleNode(nodeID, addr, logger)
	if err != nil {
		return nil, err
	}

	n.InitConsensus(peers)
	return n, nil
}

// GetFreePort returns a free port number as a string
func GetFreePort() string {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "0"
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "0"
	}
	defer l.Close()

	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}
