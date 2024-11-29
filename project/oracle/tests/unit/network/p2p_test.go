package network_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"oracle/internal/network"
	"oracle/tests"
)

func TestP2PReconnection(t *testing.T) {
	t.Log("Starting P2P reconnection test...")

	// Create nodes with unique ports
	port1 := tests.GetFreePort()
	port2 := tests.GetFreePort()
	addr1 := fmt.Sprintf("127.0.0.1:%s", port1)
	addr2 := fmt.Sprintf("127.0.0.1:%s", port2)

	t.Logf("Creating nodes with addresses %s and %s", addr1, addr2)

	// Create nodes
	node1 := network.NewP2PTransport("node1", addr1)
	node2 := network.NewP2PTransport("node2", addr2)

	// Start node1
	t.Log("Starting node1...")
	if err := node1.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start node1: %v", err)
	}
	defer node1.Stop()

	// Register peer2 on node1
	t.Logf("Registering peer2 (%s) on node1", addr2)
	if err := node1.RegisterPeer("node2", addr2); err != nil {
		t.Fatalf("Failed to register peer2 on node1: %v", err)
	}

	// Try send (should fail quickly)
	t.Log("Attempting to send message to offline peer (should fail)...")
	err := node1.Send("node2", []byte("test"))
	if err == nil {
		t.Fatal("Expected send to fail when peer is offline")
	}
	t.Logf("Send failed as expected: %v", err)

	// Start node2
	t.Log("Starting node2...")
	if err := node2.Start(context.Background()); err != nil {
		t.Fatalf("Failed to start node2: %v", err)
	}
	defer node2.Stop()

	// Register peer1 on node2
	if err := node2.RegisterPeer("node1", addr1); err != nil {
		t.Fatalf("Failed to register peer1 on node2: %v", err)
	}

	// Wait for connection establishment
	t.Log("Waiting for connection establishment...")
	time.Sleep(time.Second)

	// Try send with retries
	testMsg := []byte("test message")
	var sendErr error
	for i := 0; i < 3; i++ {
		sendErr = node1.Send("node2", testMsg)
		if sendErr == nil {
			t.Log("Message sent successfully")
			break
		}
		t.Logf("Send attempt %d failed: %v", i+1, sendErr)
		time.Sleep(500 * time.Millisecond)
	}
	if sendErr != nil {
		t.Fatalf("Failed to send message after retries: %v", sendErr)
	}

	// Check message received
	select {
	case msg := <-node2.Receive():
		if !bytes.Equal(msg.Payload, testMsg) {
			t.Fatalf("Received wrong message. Expected %s, got %s", testMsg, msg.Payload)
		}
		t.Log("Message received successfully")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message")
	}

	t.Log("Test completed successfully")
}
