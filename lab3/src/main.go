package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Constants for message types
const (
	JOIN      = 1
	REQ       = 2
	OK        = 3
	NEWVIEW   = 4
	NEWLEADER = 5
)

// Constants for operation types
const (
	ADD     = 1
	DELETE  = 2
	PENDING = 3
	NOTHING = 4
)

// Peer represents a node in the membership
type Peer struct {
	ID       int
	Hostname string
	IsAlive  bool
}

// Membership represents the current view of the group
type Membership struct {
	ViewID int
	Peers  []Peer
	mu     sync.Mutex
}

// Message represents the structure of messages exchanged between peers
type Message struct {
	Type      int // JOIN, REQ, OK, NEWVIEW
	RequestID int
	ViewID    int
	Operation int // ADD, DELETE
	PeerID    int
	Peers     []Peer // Only used in NEWVIEW messages
}

var (
	membership       *Membership
	allKnownHosts    []string
	hostnameToID     map[string]int
	idToHostname     map[int]string
	myID             int
	isLeader         bool
	leaderID         int
	lastHeartbeat    map[int]time.Time
	heartbeatMu      sync.Mutex
	removalRequests  chan int = make(chan int, 10) // Buffer for removal requests
	pendingOperation *Message
	leaderCrash      bool
)

const (
	HEARTBEAT_INTERVAL = 2 * time.Second
	FAILURE_TIMEOUT    = 2 * HEARTBEAT_INTERVAL
)

type HeartbeatMessage struct {
	PeerID int
}

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

// printMembershipUpdate outputs the current membership state
func printMembershipUpdate() {
	var memberList []string
	for _, peer := range membership.Peers {
		if peer.IsAlive {
			memberList = append(memberList, fmt.Sprintf("%d", peer.ID))
		}
	}
	fmt.Fprintf(os.Stderr, "{peer_id: %d, view_id: %d, leader: %d, memb_list: [%s]}\n",
		myID, membership.ViewID, leaderID, strings.Join(memberList, ","))
}

// main is the entry point of the program
func main() {
	// Parse command-line flags
	hostfile := flag.String("h", "hostsfile.txt", "Path to the hostfile")
	delay := flag.Int("d", 0, "Delay in seconds before starting the protocol")
	crashDelay := flag.Int("c", 0, "Delay in seconds before crashing after joining")
	leaderCrash := flag.Bool("t", false, "Enable leader crash simulation")
	flag.Parse()

	// Read hosts from file
	var err error
	allKnownHosts, err = readHostsFile(*hostfile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading hosts file: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Error: This host is not in the hostfile\n")
		return
	}
	isLeader = (myID == 1)
	leaderID = 1

	// Initialize membership
	membership = &Membership{
		ViewID: 0,
		Peers:  make([]Peer, 0),
	}

	// Initialize lastHeartbeat map
	lastHeartbeat = make(map[int]time.Time)

	// Sleep for the specified delay
	time.Sleep(time.Duration(*delay) * time.Second)

	if isLeader && *leaderCrash {
		go runLeaderCrash()
	}

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
		go joinGroup(*crashDelay)
	}

	// Start listening for incoming messages
	go listenForMessages()

	// Start heartbeat sender and listener
	go sendHeartbeats()
	go listenHeartbeats()

	// Start failure detector
	go failureDetector()

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
		case removalRequest := <-removalRequests:
			if isLeader {
				go initiateRemovePeer(removalRequest)
			}
		case <-time.After(HEARTBEAT_INTERVAL):
			// Any other periodic tasks
		}
	}
}

func initiateRemovePeer(peerID int) {
	if !isLeader {
		return
	}

	reqID := generateRequestID()
	viewID := membership.ViewID

	okResponses := make(chan bool, len(membership.Peers))
	activePeers := 0

	membership.mu.Lock()
	for _, peer := range membership.Peers {
		if peer.ID != myID && peer.IsAlive && peer.ID != peerID {
			activePeers++
			go func(p Peer) {
				ok := sendReqMessage(p, reqID, viewID, DELETE, peerID)
				okResponses <- ok
			}(peer)
		}
	}
	membership.mu.Unlock()

	// Wait for OK messages
	timeout := time.After(5 * time.Second)
	okCount := 0
	for i := 0; i < activePeers; i++ {
		select {
		case ok := <-okResponses:
			if ok {
				okCount++
			}
		case <-timeout:
			return
		}
	}

	if okCount < activePeers/2 {
		return
	}

	removePeerAndSendNewView(peerID)
}

func removePeerAndSendNewView(peerID int) {
	membership.mu.Lock()
	defer membership.mu.Unlock()

	newViewID := membership.ViewID + 1
	var updatedPeers []Peer
	for _, peer := range membership.Peers {
		if peer.ID != peerID {
			updatedPeers = append(updatedPeers, peer)
		}
	}

	membership.ViewID = newViewID
	membership.Peers = updatedPeers

	for _, peer := range updatedPeers {
		if peer.ID != myID {
			sendNewViewMessage(peer, newViewID, updatedPeers)
		}
	}

	printMembershipUpdate()
}

// handleJoinRequest processes a join request from a new peer
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
			return
		}
	}

	if okCount < (len(membership.Peers)-1)/2 {
		return
	}

	// Phase 2: Send NEWVIEW to all members including the new one
	newViewID := viewID + 1
	newPeer := Peer{ID: peerID, Hostname: idToHostname[peerID], IsAlive: true}
	updatedPeers := append(membership.Peers, newPeer)

	membership.updateMembership(updatedPeers)

	// Reset heartbeat for the new peer
	heartbeatMu.Lock()
	lastHeartbeat[peerID] = time.Now()
	heartbeatMu.Unlock()

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
		// fmt.Println("Error connecting to peer:", err)
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
		// fmt.Println("Error sending REQ message:", err)
		return false
	}

	// Wait for OK response
	response, err := receiveMessage(conn)
	if err != nil {
		// fmt.Println("Error receiving OK message:", err)
		return false
	}

	return response.Type == OK && response.RequestID == reqID && response.ViewID == viewID
}

func sendNewViewMessage(peer Peer, newViewID int, peers []Peer) {
	conn, err := net.Dial("tcp", peer.Hostname+":8080")
	if err != nil {
		// fmt.Println("Error connecting to peer:", err)
		return
	}
	defer conn.Close()

	newViewMsg := Message{
		Type:   NEWVIEW,
		ViewID: newViewID,
		Peers:  peers,
	}
	sendMessage(conn, newViewMsg)
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
			fmt.Fprintf(os.Stderr, "Error receiving message: %v\n", err)
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
	case NEWLEADER:
		handleNewLeaderMessage(msg, conn)
	}
}

func handleReqMessage(msg Message, conn net.Conn) {
	// fmt.Fprintf(os.Stderr, "Received REQ: RequestID=%d, ViewID=%d, Operation=%d, PeerID=%d\n",
	// 	msg.RequestID, msg.ViewID, msg.Operation, msg.PeerID)

	// Send OK message back to the leader
	okMsg := Message{
		Type:      OK,
		RequestID: msg.RequestID,
		ViewID:    msg.ViewID,
	}
	sendMessage(conn, okMsg)

	pendingOperation = &msg
}

func handleOkMessage(msg Message) {
	// OK messages that are received when the leader is not expecting them are ignored
	// OK responses for REQ messages are handled separately
	// fmt.Printf("Received OK: RequestID=%d, ViewID=%d\n", msg.RequestID, msg.ViewID)
}

func handleNewViewMessage(msg Message) {
	membership.mu.Lock()
	defer membership.mu.Unlock()

	if msg.ViewID <= membership.ViewID {
		return
	}

	membership.ViewID = msg.ViewID
	if msg.Peers != nil {
		membership.Peers = msg.Peers
		// Reset lastHeartbeat for the new membership
		heartbeatMu.Lock()
		newLastHeartbeat := make(map[int]time.Time)
		for _, peer := range membership.Peers {
			if peer.ID != myID && peer.IsAlive {
				if lastBeat, ok := lastHeartbeat[peer.ID]; ok {
					newLastHeartbeat[peer.ID] = lastBeat
				} else {
					newLastHeartbeat[peer.ID] = time.Now()
				}
			}
		}
		lastHeartbeat = newLastHeartbeat
		heartbeatMu.Unlock()
	}

	pendingOperation = nil
	printMembershipUpdate()
}

func generateRequestID() int {
	return int(time.Now().UnixNano())
}

var joinRequests = make(chan Message, 10) // Buffer for join requests

func joinGroup(crashDelay int) {
	leaderAddr := allKnownHosts[0] + ":8080"
	conn, err := net.Dial("tcp", leaderAddr)
	if err != nil {
		return
	}
	defer conn.Close()

	joinMsg := Message{
		Type:   JOIN,
		PeerID: myID,
	}
	sendMessage(conn, joinMsg)

	if crashDelay > 0 {
		time.Sleep(time.Duration(crashDelay) * time.Second)
		crash()
	}
}

func listenForMessages() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting listener: %v\n", err)
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accepting connection: %v\n", err)
			continue
		}
		go handleConnection(conn)
	}
}

func sendHeartbeats() {
	for {
		membership.mu.Lock()
		for _, peer := range membership.Peers {
			if peer.ID != myID && peer.IsAlive {
				go sendHeartbeat(peer)
			}
		}
		membership.mu.Unlock()
		time.Sleep(HEARTBEAT_INTERVAL)
	}
}

func sendHeartbeat(peer Peer) {
	conn, err := net.Dial("udp", peer.Hostname+":8081")
	if err != nil {
		// fmt.Printf("Error connecting to peer %d for heartbeat: %v\n", peer.ID, err)
		return
	}
	defer conn.Close()

	heartbeat := HeartbeatMessage{
		PeerID: myID,
	}

	encoder := json.NewEncoder(conn)
	err = encoder.Encode(heartbeat)
	if err != nil {
		// fmt.Printf("Error sending heartbeat to peer %d: %v\n", peer.ID, err)
	}
}

func listenHeartbeats() {
	addr, err := net.ResolveUDPAddr("udp", ":8081")
	if err != nil {
		// fmt.Println("Error resolving UDP address:", err)
		return
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		// fmt.Println("Error listening for heartbeats:", err)
		return
	}
	defer conn.Close()

	for {
		var heartbeat HeartbeatMessage
		decoder := json.NewDecoder(conn)
		err := decoder.Decode(&heartbeat)
		if err != nil {
			// fmt.Println("Error receiving heartbeat:", err)
			continue
		}

		heartbeatMu.Lock()
		lastHeartbeat[heartbeat.PeerID] = time.Now()
		heartbeatMu.Unlock()
	}
}

// failureDetector periodically checks for unreachable peers
func failureDetector() {
	for {
		time.Sleep(HEARTBEAT_INTERVAL)

		membership.mu.Lock()
		heartbeatMu.Lock()
		for _, peer := range membership.Peers {
			if peer.ID != myID && peer.IsAlive {
				lastBeat, ok := lastHeartbeat[peer.ID]
				if ok && time.Since(lastBeat) > FAILURE_TIMEOUT {
					if peer.ID == leaderID {
						fmt.Fprintf(os.Stderr, "{peer_id:%d, view_id: %d, leader: %d, message:\"peer %d (leader) unreachable\"}\n",
							myID, membership.ViewID, leaderID, peer.ID)
						handleLeaderFailure()
					} else {
						fmt.Fprintf(os.Stderr, "{peer_id:%d, view_id: %d, leader: %d, message:\"peer %d unreachable\"}\n",
							myID, membership.ViewID, leaderID, peer.ID)
						if isLeader {
							removalRequests <- peer.ID
						}
					}
				}
			}
		}
		heartbeatMu.Unlock()
		membership.mu.Unlock()
	}
}

func crash() {
	fmt.Fprintf(os.Stderr, "{peer_id:%d, view_id: %d, leader: %d, message:\"crashing\"}\n",
		myID, membership.ViewID, leaderID)
	os.Exit(0)
}

func runLeaderCrash() {
	// Wait for all hosts to join
	for len(membership.Peers) < len(allKnownHosts) {
		time.Sleep(time.Second)
	}

	// Send REQ message to all hosts except the next leader
	nextLeaderID := findNextLeaderID()
	reqID := generateRequestID()
	viewID := membership.ViewID
	peerToRemove := membership.Peers[len(membership.Peers)-1].ID

	for _, peer := range membership.Peers {
		if peer.ID != myID && peer.ID != nextLeaderID {
			sendReqMessage(peer, reqID, viewID, DELETE, peerToRemove)
		}
	}

	// Crash the leader
	crash()
}

func findNextLeaderID() int {
	minID := math.MaxInt32
	for _, peer := range membership.Peers {
		if peer.ID != leaderID && peer.IsAlive && peer.ID < minID {
			minID = peer.ID
		}
	}
	return minID
}

// handleLeaderFailure manages the process of electing a new leader
func handleLeaderFailure() {
	nextLeaderID := findNextLeaderID()
	if myID == nextLeaderID {
		isLeader = true
		leaderID = myID
		// fmt.Printf("Becoming new leader: {peer_id: %d, view_id: %d, leader: %d}\n", myID, membership.ViewID, leaderID)

		// Start leader responsibilities
		go leaderLoop()
		go initiateReconciliation()
	} else {
		leaderID = nextLeaderID
		// fmt.Printf("New leader elected: {peer_id: %d, view_id: %d, leader: %d}\n", myID, membership.ViewID, leaderID)
	}
}

// initiateReconciliation handles pending operations after a new leader is elected
func initiateReconciliation() {
	reqID := generateRequestID()
	viewID := membership.ViewID

	responses := make(chan Message, len(membership.Peers))
	for _, peer := range membership.Peers {
		if peer.ID != myID && peer.IsAlive {
			go func(p Peer) {
				resp := sendNewLeaderMessage(p, reqID, viewID)
				responses <- resp
			}(peer)
		}
	}

	timeout := time.After(5 * time.Second)
	pendingOps := make([]Message, 0)

	for i := 0; i < len(membership.Peers)-1; i++ {
		select {
		case resp := <-responses:
			if resp.Operation != NOTHING {
				pendingOps = append(pendingOps, resp)
			}
		case <-timeout:
			// fmt.Println("Timeout waiting for NEWLEADER responses")
			return
		}
	}

	if len(pendingOps) > 0 {
		// Handle the pending operation (assuming only one)
		op := pendingOps[0]
		if op.Operation == ADD {
			handleJoinRequest(op.PeerID)
		} else if op.Operation == DELETE {
			initiateRemovePeer(op.PeerID)
		}
	}

	// Update view ID and broadcast new view
	membership.mu.Lock()
	membership.ViewID++
	newViewID := membership.ViewID
	membership.mu.Unlock()

	for _, peer := range membership.Peers {
		if peer.ID != myID && peer.IsAlive {
			sendNewViewMessage(peer, newViewID, membership.Peers)
		}
	}

	printMembershipUpdate()
}

func sendNewLeaderMessage(peer Peer, reqID, viewID int) Message {
	conn, err := net.Dial("tcp", peer.Hostname+":8080")
	if err != nil {
		// fmt.Println("Error connecting to peer:", err)
		return Message{}
	}
	defer conn.Close()

	newLeaderMsg := Message{
		Type:      NEWLEADER,
		RequestID: reqID,
		ViewID:    viewID,
		Operation: PENDING,
	}
	err = sendMessage(conn, newLeaderMsg)
	if err != nil {
		// fmt.Println("Error sending NEWLEADER message:", err)
		return Message{}
	}

	response, err := receiveMessage(conn)
	if err != nil {
		// fmt.Println("Error receiving response to NEWLEADER:", err)
		return Message{}
	}

	return response
}

func handleNewLeaderMessage(msg Message, conn net.Conn) {
	var response Message
	if pendingOperation != nil {
		response = *pendingOperation
	} else {
		response = Message{
			Type:      OK,
			RequestID: msg.RequestID,
			ViewID:    msg.ViewID,
			Operation: NOTHING,
		}
	}
	sendMessage(conn, response)
}
