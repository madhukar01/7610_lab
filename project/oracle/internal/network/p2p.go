package network

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
}

// NewP2PTransport creates a new P2P transport
func NewP2PTransport(nodeID, address, version string) *P2PTransport {
	t := &P2PTransport{
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

	t.healthManager = NewHealthManager(t)
	t.discovery = NewPeerDiscovery(t, version)
	return t
}

func (t *P2PTransport) Start(ctx context.Context) error {
	if err := t.startListener(); err != nil {
		return err
	}

	t.healthManager.Start()
	t.discovery.Start()

	go t.acceptConnections(ctx)
	return nil
}

func (t *P2PTransport) Stop() error {
	t.healthManager.Stop()
	t.discovery.Stop()
	close(t.doneChan)
	return t.listener.Close()
}

func (t *P2PTransport) Send(to string, payload []byte) error {
	t.mu.RLock()
	conn, exists := t.connections[to]
	t.mu.RUnlock()

	if !exists {
		if err := t.connectWithRetry(to); err != nil {
			return fmt.Errorf("failed to establish connection: %w", err)
		}
		t.mu.RLock()
		conn = t.connections[to]
		t.mu.RUnlock()
	}

	msg := &Message{
		From:    t.nodeID,
		To:      to,
		Payload: payload,
	}

	if err := t.sendMessage(conn, msg); err != nil {
		t.closeConnection(to)
		t.handleConnectionFailure(to)
		return fmt.Errorf("failed to send message: %w", err)
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

func (t *P2PTransport) acceptConnections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.doneChan:
			return
		default:
			conn, err := t.listener.Accept()
			if err != nil {
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
			go t.handleConnection(conn)
		}
	}
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
	t.mu.Lock()
	if t.reconnecting[peerID] {
		t.mu.Unlock()
		return fmt.Errorf("already attempting to reconnect to peer %s", peerID)
	}
	t.reconnecting[peerID] = true
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.reconnecting, peerID)
		t.mu.Unlock()
	}()

	var lastErr error
	for attempt := 0; attempt < t.retryConfig.MaxAttempts; attempt++ {
		if err := t.connect(peerID); err != nil {
			lastErr = err
			delay := t.retryConfig.calculateDelay(attempt)

			select {
			case <-t.doneChan:
				return fmt.Errorf("transport shutting down")
			case <-time.After(delay):
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("failed to connect after %d attempts: %v", t.retryConfig.MaxAttempts, lastErr)
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
