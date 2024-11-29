package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// P2PTransport implements the Transport interface
type P2PTransport struct {
	mu            sync.RWMutex
	nodeID        string
	address       string
	peers         map[string]string // nodeID -> address
	listener      net.Listener
	connections   map[string]net.Conn
	msgChan       chan *Message
	doneChan      chan struct{}
	retryConfig   *RetryConfig
	reconnecting  map[string]bool
	healthManager *HealthManager
	discovery     *PeerDiscovery
	version       string
	msgHandler    MessageHandler
}

// NewP2PTransport creates a new P2P transport
func NewP2PTransport(nodeID, address string, opts ...string) *P2PTransport {
	version := "1.0.0" // default version
	if len(opts) > 0 {
		version = opts[0]
	}

	return &P2PTransport{
		nodeID:       nodeID,
		address:      address,
		peers:        make(map[string]string),
		connections:  make(map[string]net.Conn),
		msgChan:      make(chan *Message, 1000),
		doneChan:     make(chan struct{}),
		retryConfig:  DefaultRetryConfig(),
		reconnecting: make(map[string]bool),
		version:      version,
	}
}

func (t *P2PTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Create listener
	listener, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}
	t.listener = listener

	// Start accept loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.doneChan:
				return
			default:
				conn, err := listener.Accept()
				if err != nil {
					if !strings.Contains(err.Error(), "use of closed") {
						log.Printf("Accept error: %v", err)
					}
					return
				}
				go t.handleConnection(conn)
			}
		}
	}()

	return nil
}

func (t *P2PTransport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Signal shutdown
	close(t.doneChan)

	// Close listener
	if t.listener != nil {
		t.listener.Close()
	}

	// Close all connections
	for _, conn := range t.connections {
		conn.Close()
	}

	// Clear connections map
	t.connections = make(map[string]net.Conn)

	return nil
}

func (t *P2PTransport) Send(peerID string, payload []byte) error {
	// Try to get existing connection first
	t.mu.RLock()
	conn, exists := t.connections[peerID]
	t.mu.RUnlock()

	if !exists {
		if err := t.connectWithRetry(peerID); err != nil {
			return fmt.Errorf("failed to establish connection: %w", err)
		}
		t.mu.RLock()
		conn = t.connections[peerID]
		t.mu.RUnlock()
	}

	// Send the message
	msg := &Message{
		From:    t.nodeID,
		To:      peerID,
		Payload: payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write with deadline
	deadline := time.Now().Add(500 * time.Millisecond)
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	// Write message length
	length := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		t.mu.Lock()
		delete(t.connections, peerID)
		t.mu.Unlock()
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Write message data
	if _, err := conn.Write(data); err != nil {
		t.mu.Lock()
		delete(t.connections, peerID)
		t.mu.Unlock()
		return fmt.Errorf("failed to write message data: %w", err)
	}

	return nil
}

func (t *P2PTransport) Broadcast(payload []byte) error {
	t.mu.RLock()
	peers := make([]string, 0, len(t.peers))
	for peerID := range t.peers {
		peers = append(peers, peerID)
	}
	t.mu.RUnlock()

	for _, peerID := range peers {
		if err := t.Send(peerID, payload); err != nil {
			log.Printf("Failed to send to peer %s: %v", peerID, err)
		}
	}

	return nil
}

func (t *P2PTransport) Receive() <-chan *Message {
	return t.msgChan
}

func (t *P2PTransport) RegisterPeer(id string, address string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.peers[id] = address
	return nil
}

func (t *P2PTransport) handleConnection(conn net.Conn) {
	var peerID string
	t.mu.RLock()
	for id, c := range t.connections {
		if c == conn {
			peerID = id
			break
		}
	}
	t.mu.RUnlock()

	defer func() {
		conn.Close()
		if peerID != "" {
			t.closeConnection(peerID)
			t.handleConnectionFailure(peerID)
		}
	}()

	for {
		select {
		case <-t.doneChan:
			return
		default:
			msg, err := ReadMessage(conn)
			if err != nil {
				if err != io.EOF {
					log.Printf("Failed to read message: %v", err)
				}
				return
			}

			select {
			case t.msgChan <- msg:
			case <-t.doneChan:
				return
			}
		}
	}
}

func (t *P2PTransport) connectWithRetry(peerID string) error {
	t.mu.RLock()
	peerAddr, exists := t.peers[peerID]
	t.mu.RUnlock()

	if !exists {
		return fmt.Errorf("unknown peer: %s", peerID)
	}

	// Check if already connected
	t.mu.RLock()
	_, connected := t.connections[peerID]
	t.mu.RUnlock()
	if connected {
		return nil
	}

	// Single connection attempt with short timeout
	conn, err := net.DialTimeout("tcp", peerAddr, 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	t.mu.Lock()
	t.connections[peerID] = conn
	t.mu.Unlock()

	go t.handleConnection(conn)
	return nil
}

func (t *P2PTransport) handleConnectionFailure(peerID string) {
	go func() {
		if err := t.connectWithRetry(peerID); err != nil {
			log.Printf("Failed to reconnect to peer %s: %v", peerID, err)
		}
	}()
}

func (t *P2PTransport) connect(peerID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	address, exists := t.peers[peerID]
	if !exists {
		return fmt.Errorf("unknown peer: %s", peerID)
	}

	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}

	t.connections[peerID] = conn
	go t.handleConnection(conn)
	return nil
}

func (t *P2PTransport) sendMessage(conn net.Conn, msg *Message) error {
	return WriteMessage(conn, msg)
}

func (t *P2PTransport) closeConnection(peerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if conn, exists := t.connections[peerID]; exists {
		conn.Close()
		delete(t.connections, peerID)
	}
}

func (t *P2PTransport) reconnect(peerID string) error {
	t.closeConnection(peerID)
	return t.connect(peerID)
}

func (t *P2PTransport) handleMessage(msg *Message) error {
	var rawMsg map[string]interface{}
	if err := json.Unmarshal(msg.Payload, &rawMsg); err != nil {
		return err
	}

	msgType, ok := rawMsg["type"].(string)
	if !ok {
		return fmt.Errorf("invalid message type")
	}

	switch msgType {
	case "discovery":
		var discoveryMsg DiscoveryMessage
		if err := json.Unmarshal(msg.Payload, &discoveryMsg); err != nil {
			return err
		}
		t.discovery.HandleDiscoveryMessage(&discoveryMsg)
	case "ping":
		// Send pong response
		pong := &HealthCheck{
			Type:   "pong",
			Time:   time.Now().UnixNano(),
			NodeID: t.nodeID,
		}
		data, _ := json.Marshal(pong)
		return t.Send(msg.From, data)
	default:
		return t.msgHandler(msg)
	}

	return nil
}

func (t *P2PTransport) SetMessageHandler(handler MessageHandler) {
	t.msgHandler = handler
}

// Add this method to get the node ID
func (t *P2PTransport) NodeID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nodeID
}
