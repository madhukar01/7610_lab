package network

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

const (
	discoveryInterval = 30 * time.Second
	peerTTL           = 5 * time.Minute
)

type PeerInfo struct {
	NodeID   string    `json:"node_id"`
	Address  string    `json:"address"`
	LastSeen time.Time `json:"last_seen"`
	Version  string    `json:"version"`
}

type DiscoveryMessage struct {
	Type    string     `json:"type"`
	Peers   []PeerInfo `json:"peers"`
	NodeID  string     `json:"node_id"`
	Address string     `json:"address"`
}

type PeerDiscovery struct {
	mu         sync.RWMutex
	transport  *P2PTransport
	knownPeers map[string]*PeerInfo
	stopChan   chan struct{}
	version    string
}

func NewPeerDiscovery(transport *P2PTransport, version string) *PeerDiscovery {
	return &PeerDiscovery{
		transport:  transport,
		knownPeers: make(map[string]*PeerInfo),
		stopChan:   make(chan struct{}),
		version:    version,
	}
}

func (pd *PeerDiscovery) Start() {
	go pd.discoveryLoop()
}

func (pd *PeerDiscovery) Stop() {
	close(pd.stopChan)
}

func (pd *PeerDiscovery) discoveryLoop() {
	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pd.stopChan:
			return
		case <-ticker.C:
			pd.broadcastPeers()
			pd.cleanupStaleNodes()
		}
	}
}

func (pd *PeerDiscovery) broadcastPeers() {
	pd.mu.RLock()
	peers := make([]PeerInfo, 0, len(pd.knownPeers))
	for _, peer := range pd.knownPeers {
		peers = append(peers, *peer)
	}
	pd.mu.RUnlock()

	msg := DiscoveryMessage{
		Type:    "discovery",
		Peers:   peers,
		NodeID:  pd.transport.nodeID,
		Address: pd.transport.address,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal discovery message: %v", err)
		return
	}

	pd.transport.Broadcast(data)
}

func (pd *PeerDiscovery) HandleDiscoveryMessage(msg *DiscoveryMessage) {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	// Update sender's info
	pd.knownPeers[msg.NodeID] = &PeerInfo{
		NodeID:   msg.NodeID,
		Address:  msg.Address,
		LastSeen: time.Now(),
		Version:  pd.version,
	}

	// Process received peers
	for _, peer := range msg.Peers {
		if peer.NodeID == pd.transport.nodeID {
			continue
		}

		existing, exists := pd.knownPeers[peer.NodeID]
		if !exists || existing.LastSeen.Before(peer.LastSeen) {
			pd.knownPeers[peer.NodeID] = &peer
			pd.transport.RegisterPeer(peer.NodeID, peer.Address)
		}
	}
}

func (pd *PeerDiscovery) cleanupStaleNodes() {
	pd.mu.Lock()
	defer pd.mu.Unlock()

	now := time.Now()
	for nodeID, peer := range pd.knownPeers {
		if now.Sub(peer.LastSeen) > peerTTL {
			delete(pd.knownPeers, nodeID)
			pd.transport.closeConnection(nodeID)
		}
	}
}
