package network

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

// P2PTransport implements the Transport interface
type P2PTransport struct {
	mu          sync.RWMutex
	nodeID      string
	address     string
	peers       map[string]string // nodeID -> address
	listener    net.Listener
	connections map[string]net.Conn
	msgChan     chan *Message
	doneChan    chan struct{}
}

// NewP2PTransport creates a new P2P transport
func NewP2PTransport(nodeID, address string) *P2PTransport {
	return &P2PTransport{
		nodeID:      nodeID,
		address:     address,
		peers:       make(map[string]string),
		connections: make(map[string]net.Conn),
		msgChan:     make(chan *Message, 1000),
		doneChan:    make(chan struct{}),
	}
}

func (t *P2PTransport) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", t.address)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}
	t.listener = listener

	go t.acceptConnections(ctx)
	return nil
}

func (t *P2PTransport) Stop() error {
	close(t.doneChan)
	if t.listener != nil {
		return t.listener.Close()
	}
	return nil
}

func (t *P2PTransport) Send(to string, payload []byte) error {
	t.mu.RLock()
	conn, exists := t.connections[to]
	t.mu.RUnlock()

	if !exists {
		// Try to establish connection
		if err := t.connect(to); err != nil {
			return fmt.Errorf("failed to connect to peer: %w", err)
		}
		conn = t.connections[to]
	}

	msg := &Message{
		From:    t.nodeID,
		To:      to,
		Payload: payload,
	}

	// Send message
	if err := t.sendMessage(conn, msg); err != nil {
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
	defer conn.Close()

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

			// Send message to channel
			select {
			case t.msgChan <- msg:
			case <-t.doneChan:
				return
			}
		}
	}
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
