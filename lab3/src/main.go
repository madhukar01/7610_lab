package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Message types
const (
	JOIN    = 1
	REQ     = 2
	OK      = 3
	NEWVIEW = 4
)

// Operation types
const (
	ADD    = 1
	DELETE = 2
)

type Peer struct {
	ID       int
	Hostname string
	IsAlive  bool
}

type Membership struct {
	ViewID int
	Peers  []Peer
	mu     sync.Mutex
}

type Message struct {
	Type      int // JOIN, REQ, OK, NEWVIEW
	RequestID int
	ViewID    int
	Operation int // ADD, DELETE
	PeerID    int
	Peers     []Peer // Only used in NEWVIEW messages
}

var (
	membership    *Membership
	allKnownHosts []string
	hostnameToID  map[string]int
	idToHostname  map[int]string
	myID          int
	isLeader      bool
	leaderID      int
)

func (m *Membership) updateMembership(alivePeers []Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ViewID++
	m.Peers = alivePeers

	// Sort peers by ID
	sort.Slice(m.Peers, func(i, j int) bool {
		return m.Peers[i].ID < m.Peers[j].ID
	})

	// Print membership update
	printMembershipUpdate()
}

func printMembershipUpdate() {
	var memberList []string
	for _, peer := range membership.Peers {
		if peer.IsAlive {
			memberList = append(memberList, fmt.Sprintf("%d", peer.ID))
		}
	}
	fmt.Printf("{peer_id: %d, view_id: %d, leader: %d, memb_list: [%s]}\n",
		myID, membership.ViewID, leaderID, strings.Join(memberList, ","))
}

func main() {
	// Define and parse command-line flags
	hostfile := flag.String("h", "hostsfile.txt", "Path to the hostfile")
	delay := flag.Int("d", 0, "Delay in seconds before starting the protocol")
	flag.Parse()

	// Read hosts from file
	var err error
	allKnownHosts, err = readHostsFile(*hostfile)
	if err != nil {
		log.Fatal("Error reading hosts file:", err)
		return
	}

	// populate maps
	hostnameToID = make(map[string]int)
	idToHostname = make(map[int]string)
	for i, hostname := range allKnownHosts {
		hostnameToID[hostname] = i + 1
		idToHostname[i+1] = hostname
	}

	// Determine this peer's ID and if it's the leader
	hostname, _ := os.Hostname()
	myID = hostnameToID[hostname]
	if myID == 0 {
		fmt.Println("Error: This host is not in the hostfile")
		return
	}
	isLeader = (myID == 1)
	leaderID = 1

	// Initialize membership
	membership = &Membership{
		ViewID: 0,
		Peers:  make([]Peer, 0),
	}

	// Sleep for the specified delay
	time.Sleep(time.Duration(*delay) * time.Second)

	if isLeader {
		// Leader starts with itself in the membership list
		membership.Peers = append(membership.Peers, Peer{
			ID:       myID,
			Hostname: idToHostname[myID],
			IsAlive:  true,
		})
		printMembershipUpdate()
		go leaderLoop()
	} else {
		go joinGroup()
	}

	// Start listening for incoming messages
	go listenForMessages()

	// Keep the program running
	select {}
}

func readHostsFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hosts []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		hosts = append(hosts, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hosts, nil
}

func leaderLoop() {
	for {
		select {
		case joinRequest := <-joinRequests:
			handleJoinRequest(joinRequest.PeerID)
		case <-time.After(5 * time.Second):
			// Periodic membership check (simplified)
			checkMembership()
		}
	}
}

func handleJoinRequest(peerID int) {
	reqID := generateRequestID()
	viewID := membership.ViewID

	// Phase 1: Send REQ to all existing members
	okResponses := make(chan bool, len(membership.Peers))
	for _, peer := range membership.Peers {
		if peer.IsAlive && peer.ID != myID {
			go func(p Peer) {
				ok := sendReqMessage(p, reqID, viewID, ADD, peerID)
				okResponses <- ok
			}(peer)
		}
	}

	// Wait for OK messages
	timeout := time.After(5 * time.Second)
	okCount := 0
	for i := 0; i < len(membership.Peers)-1; i++ {
		select {
		case ok := <-okResponses:
			if ok {
				okCount++
			}
		case <-timeout:
			fmt.Println("Timeout waiting for OK messages")
			return
		}
	}

	if okCount < (len(membership.Peers)-1)/2 {
		fmt.Println("Did not receive majority of OK messages")
		return
	}

	// Phase 2: Send NEWVIEW to all members including the new one
	newViewID := viewID + 1
	newPeer := Peer{ID: peerID, Hostname: idToHostname[peerID], IsAlive: true}
	updatedPeers := append(membership.Peers, newPeer)

	membership.updateMembership(updatedPeers)

	// Send NEWVIEW to all members including the new one
	for _, peer := range updatedPeers {
		if peer.IsAlive {
			sendNewViewMessage(peer, newViewID, updatedPeers)
		}
	}
}

func sendReqMessage(peer Peer, reqID, viewID int, operation int, peerID int) bool {
	conn, err := net.Dial("tcp", peer.Hostname+":8080")
	if err != nil {
		fmt.Println("Error connecting to peer:", err)
		return false
	}
	defer conn.Close()

	reqMsg := Message{
		Type:      REQ,
		RequestID: reqID,
		ViewID:    viewID,
		Operation: operation,
		PeerID:    peerID,
	}
	err = sendMessage(conn, reqMsg)
	if err != nil {
		fmt.Println("Error sending REQ message:", err)
		return false
	}

	// Wait for OK response
	response, err := receiveMessage(conn)
	if err != nil {
		fmt.Println("Error receiving OK message:", err)
		return false
	}

	return response.Type == OK && response.RequestID == reqID && response.ViewID == viewID
}

func sendNewViewMessage(peer Peer, newViewID int, peers []Peer) {
	conn, err := net.Dial("tcp", peer.Hostname+":8080")
	if err != nil {
		fmt.Println("Error connecting to peer:", err)
		return
	}
	defer conn.Close()

	newViewMsg := Message{
		Type:   NEWVIEW,
		ViewID: newViewID,
		Peers:  peers,
	}
	err = sendMessage(conn, newViewMsg)
	if err != nil {
		fmt.Println("Error sending NEWVIEW message:", err)
	}
}

func sendMessage(conn net.Conn, msg Message) error {
	encoder := json.NewEncoder(conn)
	return encoder.Encode(msg)
}

func receiveMessage(conn net.Conn) (Message, error) {
	var msg Message
	decoder := json.NewDecoder(conn)
	err := decoder.Decode(&msg)
	return msg, err
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	msg, err := receiveMessage(conn)
	if err != nil {
		if err != io.EOF {
			fmt.Println("Error receiving message:", err)
		}
		return
	}

	switch msg.Type {
	case JOIN:
		if isLeader {
			handleJoinRequest(msg.PeerID)
		}
	case REQ:
		handleReqMessage(msg, conn)
	case OK:
		if isLeader {
			handleOkMessage(msg)
		}
	case NEWVIEW:
		handleNewViewMessage(msg)
	}
}

func handleReqMessage(msg Message, conn net.Conn) {
	// Save the operation (simplified; should use a proper data structure in a real implementation)
	fmt.Printf("Received REQ: RequestID=%d, ViewID=%d, Operation=%d, PeerID=%d\n", msg.RequestID, msg.ViewID, msg.Operation, msg.PeerID)

	// Send OK message back to the leader
	okMsg := Message{
		Type:      OK,
		RequestID: msg.RequestID,
		ViewID:    msg.ViewID,
	}
	err := sendMessage(conn, okMsg)
	if err != nil {
		fmt.Println("Error sending OK message:", err)
	}
}

func handleOkMessage(msg Message) {
	// Process OK message (simplified; should use a proper tracking mechanism in a real implementation)
	fmt.Printf("Received OK: RequestID=%d, ViewID=%d\n", msg.RequestID, msg.ViewID)
}

func handleNewViewMessage(msg Message) {
	membership.mu.Lock()
	defer membership.mu.Unlock()

	if msg.ViewID <= membership.ViewID {
		fmt.Println("Received outdated or duplicate NEWVIEW message")
		return
	}

	membership.ViewID = msg.ViewID
	if msg.Peers != nil {
		membership.Peers = msg.Peers
	}

	printMembershipUpdate()
}

func generateRequestID() int {
	return int(time.Now().UnixNano())
}

func checkMembership() {
	// Simplified membership check (ping all peers)
	for _, peer := range membership.Peers {
		if peer.ID != myID {
			conn, err := net.DialTimeout("tcp", peer.Hostname+":8080", time.Second)
			if err != nil {
				fmt.Printf("Peer %d (%s) is unreachable\n", peer.ID, peer.Hostname)
				// In a real implementation, you would initiate a protocol to remove this peer
			} else {
				conn.Close()
			}
		}
	}
}

var joinRequests = make(chan Message, 10) // Buffer for join requests

func joinGroup() {
	leaderAddr := allKnownHosts[0] + ":8080"
	conn, err := net.Dial("tcp", leaderAddr)
	if err != nil {
		fmt.Println("Error connecting to leader:", err)
		return
	}
	defer conn.Close()

	joinMsg := Message{
		Type:   JOIN,
		PeerID: myID,
	}
	sendMessage(conn, joinMsg)
}

func listenForMessages() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Println("Error starting listener:", err)
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}
		go handleConnection(conn)
	}
}
