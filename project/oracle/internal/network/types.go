package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MessageHandler is a function type that handles incoming messages
type MessageHandler func(*Message) error

// MaxMessageSize is the maximum size of a message in bytes
const MaxMessageSize = 10 * 1024 * 1024 // 10MB

// Message represents a network message
type Message struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Payload []byte `json:"payload"`
}

// Encode serializes the message to a byte slice
func (m *Message) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// Decode deserializes the message from a byte slice
func (m *Message) Decode(data []byte) error {
	return json.Unmarshal(data, m)
}

// WriteMessage writes a message to a writer
func WriteMessage(w io.Writer, msg *Message) error {
	data, err := msg.Encode()
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	// Write message length
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Write message data
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write message data: %w", err)
	}

	return nil
}

// ReadMessage reads a message from a reader
func ReadMessage(r io.Reader) (*Message, error) {
	// Read message length
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("failed to read message length: %w", err)
	}

	// Check message size
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d > %d", length, MaxMessageSize)
	}

	// Read message data
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("failed to read message data: %w", err)
	}

	// Decode message
	msg := &Message{}
	if err := msg.Decode(data); err != nil {
		return nil, fmt.Errorf("failed to decode message: %w", err)
	}

	return msg, nil
}

// Transport defines the interface for network communication
type Transport interface {
	// Start starts the network transport
	Start(ctx context.Context) error

	// Stop stops the network transport
	Stop() error

	// Send sends a message to a specific node
	Send(to string, payload []byte) error

	// Broadcast sends a message to all nodes
	Broadcast(payload []byte) error

	// Receive returns a channel for receiving messages
	Receive() <-chan *Message

	// RegisterPeer registers a new peer
	RegisterPeer(id string, address string) error
}
