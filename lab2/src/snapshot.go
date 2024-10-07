package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"
)

// SnapshotState represents the state of a snapshot for a process
type SnapshotState struct {
	ID               int              // Unique identifier for the snapshot
	State            int              // Current state of the process at the time of snapshot
	HasToken         bool             // Indicates if the process currently holds the token
	ClosedChannels   map[int]bool     // Tracks which channels have been closed
	RecordedMessages map[int][]string // Stores messages received on each channel
	mutex            sync.Mutex       // Mutex for synchronizing access to snapshot state
}

// initializeSnapshot initializes a new snapshot for the process
func (p *Process) initializeSnapshot(snapshotID int) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Initialize the Snapshots map if it doesn't exist
	if p.Snapshots == nil {
		p.Snapshots = make(map[int]*SnapshotState)
	}

	// Create a new snapshot state and store it in the map
	p.Snapshots[snapshotID] = &SnapshotState{
		ID:               snapshotID,
		State:            p.State,
		HasToken:         p.HasToken,
		ClosedChannels:   make(map[int]bool),
		RecordedMessages: make(map[int][]string),
	}

	fmt.Fprintf(os.Stderr, "{proc_id:%d, snapshot_id: %d, snapshot:\"started\"}\n", p.ID, snapshotID)

	// Start sending markers for the new snapshot
	go p.sendMarkers(snapshotID)
}

// handleMarker processes a received marker message
func (p *Process) handleMarker(senderID, snapshotID int) {

	// Check if the snapshot already exists; if not, initialize it
	snapshot, exists := p.Snapshots[snapshotID]
	if !exists {
		p.initializeSnapshot(snapshotID)
		snapshot = p.Snapshots[snapshotID]
	}

	snapshot.mutex.Lock()
	defer snapshot.mutex.Unlock()

	// If the channel from the sender has not been closed, close it and print the message
	if !snapshot.ClosedChannels[senderID] {
		snapshot.ClosedChannels[senderID] = true
		fmt.Fprintf(os.Stderr, "{proc_id:%d, snapshot_id: %d, snapshot:\"channel closed\", channel:%d-%d, queue:[%s]}\n",
			p.ID, snapshotID, senderID, p.ID, joinMessages(snapshot.RecordedMessages[senderID]))

		// Check if all channels are closed; if so, print completion message and delete snapshot
		if len(snapshot.ClosedChannels) == len(p.Connections) {
			fmt.Fprintf(os.Stderr, "{proc_id:%d, snapshot_id: %d, snapshot:\"complete\"}\n", p.ID, snapshotID)
			delete(p.Snapshots, snapshotID)
		}
	}
}

// sendMarkers sends marker messages to all outgoing connections
func (p *Process) sendMarkers(snapshotID int) {
	// Wait for the specified delay before sending markers
	time.Sleep(time.Duration(p.SnapshotDelay * float64(time.Second)))
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	// Iterate over all connections and send marker messages
	packedMsg := packMessage(MARKER_MSG, snapshotID)
	for id, conn := range p.Connections {
		err := binary.Write(conn, binary.BigEndian, packedMsg)
		if err == nil {
			hasTokenStr := "NO"
			if p.HasToken {
				hasTokenStr = "YES"
			}
			fmt.Fprintf(os.Stderr, "{proc_id:%d, snapshot_id: %d, sender:%d, receiver:%d, msg:\"marker\", state:%d, has_token:%s}\n",
				p.ID, snapshotID, p.ID, id, p.State, hasTokenStr)
		}
	}
}

// recordMessage records a message received on a channel
func (p *Process) recordMessage(senderID int, message string) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	// Iterate over all active snapshots and record the message if the channel is open
	for _, snapshot := range p.Snapshots {
		snapshot.mutex.Lock()
		if !snapshot.ClosedChannels[senderID] {
			snapshot.RecordedMessages[senderID] = append(snapshot.RecordedMessages[senderID], message)
		}
		snapshot.mutex.Unlock()
	}
}

// joinMessages converts a slice of messages into a comma-separated string
func joinMessages(messages []string) string {
	result := ""
	for i, msg := range messages {
		if i > 0 {
			result += ","
		}
		result += msg
	}
	return result
}
