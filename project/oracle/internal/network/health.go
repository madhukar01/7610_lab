package network

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	healthCheckInterval = 10 * time.Second
	healthCheckTimeout  = 5 * time.Second
)

type PeerHealth struct {
	LastSeen  time.Time
	Latency   time.Duration
	Failures  int
	IsHealthy bool
}

type HealthCheck struct {
	Type   string `json:"type"`
	Time   int64  `json:"time"`
	NodeID string `json:"node_id"`
}

type HealthManager struct {
	mu         sync.RWMutex
	peerHealth map[string]*PeerHealth
	transport  *P2PTransport
	stopChan   chan struct{}
}

func NewHealthManager(transport *P2PTransport) *HealthManager {
	return &HealthManager{
		peerHealth: make(map[string]*PeerHealth),
		transport:  transport,
		stopChan:   make(chan struct{}),
	}
}

func (hm *HealthManager) Start() {
	go hm.healthCheckLoop()
}

func (hm *HealthManager) Stop() {
	close(hm.stopChan)
}

func (hm *HealthManager) healthCheckLoop() {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopChan:
			return
		case <-ticker.C:
			hm.checkAllPeers()
		}
	}
}

func (hm *HealthManager) checkAllPeers() {
	hm.transport.mu.RLock()
	peers := make([]string, 0, len(hm.transport.peers))
	for peerID := range hm.transport.peers {
		peers = append(peers, peerID)
	}
	hm.transport.mu.RUnlock()

	for _, peerID := range peers {
		go hm.checkPeer(peerID)
	}
}

func (hm *HealthManager) checkPeer(peerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return
	default:
		startTime := time.Now()

		healthCheck := &HealthCheck{
			Type:   "ping",
			Time:   time.Now().UnixNano(),
			NodeID: hm.transport.nodeID,
		}

		data, _ := json.Marshal(healthCheck)
		err := hm.transport.Send(peerID, data)

		hm.mu.Lock()
		defer hm.mu.Unlock()

		health, exists := hm.peerHealth[peerID]
		if !exists {
			health = &PeerHealth{}
			hm.peerHealth[peerID] = health
		}

		if err != nil {
			health.Failures++
			health.IsHealthy = false
		} else {
			health.Latency = time.Since(startTime)
			health.LastSeen = time.Now()
			health.Failures = 0
			health.IsHealthy = true
		}
	}
}
