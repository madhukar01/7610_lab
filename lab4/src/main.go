package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const PORT = "8080"

type Message struct {
	PeerID       int    `json:"peer_id"`
	Action       string `json:"action"`
	MessageType  string `json:"message_type"`
	MessageValue string `json:"message_value"`
	ProposalNum  int    `json:"proposal_num"`
}

type Peer struct {
	ID               int
	Roles            []string
	MinProposal      int
	AcceptedValue    string
	AcceptedNum      int
	ProposedValue    string
	Connections      map[string]net.Conn
	mutex            sync.Mutex
	prepareResponses map[int]map[int]Message // map[proposalNum]map[peerID]response
	acceptResponses  map[int]map[int]Message
	numAcceptors     int
	PeerRoles        map[string][]string
	hasChosen        bool
}

func main() {
	// Parse command line arguments
	hostsFile := flag.String("h", "", "Path to hosts file")
	value := flag.String("v", "", "Proposed value")
	// delay := flag.Int("t", 0, "Delay for proposer 2")
	flag.Parse()

	if *hostsFile == "" {
		fmt.Println("Hosts file is required")
		os.Exit(1)
	}

	// Read and parse hosts file
	hosts, err := parseHostsFile(*hostsFile)
	if err != nil {
		fmt.Printf("Error parsing hosts file: %v\n", err)
		os.Exit(1)
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Printf("Error getting hostname: %v\n", err)
		os.Exit(1)
	}

	// Initialize peer
	peer := &Peer{
		ID:               getPeerID(hostname, hosts),
		Roles:            hosts[hostname],
		Connections:      make(map[string]net.Conn),
		prepareResponses: make(map[int]map[int]Message),
		acceptResponses:  make(map[int]map[int]Message),
		numAcceptors:     countAcceptors(hosts), // Initialize number of acceptors
		PeerRoles:        hosts,
	}

	// Set the proposed value if this peer is a proposer
	if isProposer(peer.Roles) && *value != "" {
		peer.ProposedValue = *value
	}

	// Start server
	go startServer(peer)

	// Wait for other peers to start and establish connections
	time.Sleep(1 * time.Second)
	establishConnections(peer, hosts, hostname)

	// If this is proposer1, start the Paxos protocol
	if containsRole(peer.Roles, "proposer1") {
		startPaxos(peer)
	}

	// Keep the program running
	select {}
}

func isProposer(roles []string) bool {
	for _, role := range roles {
		if strings.HasPrefix(role, "proposer") {
			return true
		}
	}
	return false
}

func parseHostsFile(filepath string) (map[string][]string, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	hosts := make(map[string][]string)
	lines := strings.Split(string(content), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}

		hostname := strings.TrimSpace(parts[0])
		roles := strings.Split(strings.TrimSpace(parts[1]), ",")
		for i, role := range roles {
			roles[i] = strings.TrimSpace(role)
		}
		hosts[hostname] = roles
	}

	return hosts, nil
}

func getPeerID(hostname string, hosts map[string][]string) int {
	// Create a sorted list of hostnames
	var hostnames []string
	for host := range hosts {
		hostnames = append(hostnames, host)
	}
	sort.Strings(hostnames) // Sort hostnames alphabetically

	// Find position in sorted list (1-based indexing)
	for i, host := range hostnames {
		if host == hostname {
			return i + 1
		}
	}
	return -1
}

func containsRole(roles []string, targetRole string) bool {
	for _, role := range roles {
		if role == targetRole {
			return true
		}
	}
	return false
}

func startServer(peer *Peer) {
	listener, err := net.Listen("tcp", ":"+PORT)
	if err != nil {
		fmt.Printf("Error starting server: %v\n", err)
		os.Exit(1)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v\n", err)
			continue
		}
		go handleConnection(peer, conn)
	}
}

func establishConnections(peer *Peer, hosts map[string][]string, currentHostname string) {
	for hostname := range hosts {
		if hostname == currentHostname {
			continue
		}

		conn, err := net.Dial("tcp", hostname+":"+PORT)
		if err != nil {
			fmt.Printf("Error connecting to %s: %v\n", hostname, err)
			continue
		}

		peer.mutex.Lock()
		peer.Connections[hostname] = conn
		peer.mutex.Unlock()
	}
}

func handleConnection(peer *Peer, conn net.Conn) {
	decoder := json.NewDecoder(conn)
	for {
		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			return
		}

		handleMessage(peer, msg)
	}
}

func broadcastToAcceptors(peer *Peer, msg Message) {
	msgBytes, _ := json.Marshal(msg)

	for hostname, conn := range peer.Connections {
		// Skip if it's the current peer (comparing peer ID is more reliable than hostname)
		if getPeerID(hostname, peer.PeerRoles) == peer.ID {
			continue
		}
		// Only send to peers that are acceptors for this proposer
		if isAcceptorFor(peer.PeerRoles[hostname], peer.ID) {
			conn.Write(msgBytes)
		}
	}
}

func sendMessage(conn net.Conn, msg Message) error {
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = conn.Write(msgBytes)
	return err
}

func logMessage(msg Message) {
	jsonMsg, _ := json.Marshal(msg)
	fmt.Println(string(jsonMsg))
}

func startPaxos(peer *Peer) {
	// Start with proposal number = peerID to ensure uniqueness
	proposalNum := peer.ID

	// Cleanup old responses before starting new round
	cleanupResponses(peer, proposalNum)

	// Initialize new response maps for this proposal
	peer.mutex.Lock()
	peer.prepareResponses[proposalNum] = make(map[int]Message)
	peer.acceptResponses[proposalNum] = make(map[int]Message)
	peer.mutex.Unlock()

	prepareMsg := Message{
		PeerID:       peer.ID,
		Action:       "sent",
		MessageType:  "prepare",
		MessageValue: "",
		ProposalNum:  proposalNum,
	}

	logMessage(prepareMsg)
	broadcastToAcceptors(peer, prepareMsg)
}

func handleMessage(peer *Peer, msg Message) {
	peer.mutex.Lock()
	defer peer.mutex.Unlock()

	logMessage(Message{
		PeerID:       peer.ID,
		Action:       "received",
		MessageType:  msg.MessageType,
		MessageValue: msg.MessageValue,
		ProposalNum:  msg.ProposalNum,
	})

	switch msg.MessageType {
	case "prepare":
		handlePrepare(peer, msg)
	case "prepare_ack":
		handlePrepareAck(peer, msg)
	case "accept":
		handleAccept(peer, msg)
	case "accept_ack":
		handleAcceptAck(peer, msg)
	}
}

func handlePrepare(peer *Peer, msg Message) {
	// Only handle if we're an acceptor for this proposer
	if !isAcceptorFor(peer.Roles, msg.PeerID) {
		return
	}

	if msg.ProposalNum > peer.MinProposal {
		peer.MinProposal = msg.ProposalNum

		response := Message{
			PeerID:       peer.ID,
			Action:       "sent",
			MessageType:  "prepare_ack",
			MessageValue: peer.AcceptedValue,
			ProposalNum:  msg.ProposalNum,
		}

		logMessage(response)
		// Send response back to proposer
		for hostname, conn := range peer.Connections {
			// Only send to the original proposer
			if containsRole(peer.PeerRoles[hostname], fmt.Sprintf("proposer%d", msg.PeerID)) {
				sendMessage(conn, response)
			}
		}
	}
}

func handlePrepareAck(peer *Peer, msg Message) {
	if !isProposer(peer.Roles) {
		return
	}

	if _, exists := peer.prepareResponses[msg.ProposalNum]; !exists {
		peer.prepareResponses[msg.ProposalNum] = make(map[int]Message)
	}
	peer.prepareResponses[msg.ProposalNum][msg.PeerID] = msg

	// Check if we have majority
	if len(peer.prepareResponses[msg.ProposalNum]) > peer.numAcceptors/2 {
		// Find highest accepted proposal among responses
		highestAcceptedNum := 0
		var valueToPropose string = peer.ProposedValue

		for _, response := range peer.prepareResponses[msg.ProposalNum] {
			if response.ProposalNum > highestAcceptedNum && response.MessageValue != "" {
				highestAcceptedNum = response.ProposalNum
				valueToPropose = response.MessageValue
			}
		}

		// Send accept message
		acceptMsg := Message{
			PeerID:       peer.ID,
			Action:       "sent",
			MessageType:  "accept",
			MessageValue: valueToPropose,
			ProposalNum:  msg.ProposalNum,
		}

		logMessage(acceptMsg)
		broadcastToAcceptors(peer, acceptMsg)
	}
}

func handleAccept(peer *Peer, msg Message) {
	// Only handle if we're an acceptor for this proposer
	if !isAcceptorFor(peer.Roles, msg.PeerID) {
		return
	}

	if msg.ProposalNum >= peer.MinProposal {
		peer.MinProposal = msg.ProposalNum
		peer.AcceptedNum = msg.ProposalNum
		peer.AcceptedValue = msg.MessageValue

		response := Message{
			PeerID:       peer.ID,
			Action:       "sent",
			MessageType:  "accept_ack",
			MessageValue: peer.AcceptedValue,
			ProposalNum:  peer.AcceptedNum,
		}

		logMessage(response)
		// Send response only to the proposer
		for hostname, conn := range peer.Connections {
			if containsRole(peer.PeerRoles[hostname], fmt.Sprintf("proposer%d", msg.PeerID)) {
				sendMessage(conn, response)
				break // Only need to send to one proposer
			}
		}

		// Log chose message only once
		if !peer.hasChosen {
			logMessage(Message{
				PeerID:       peer.ID,
				Action:       "chose",
				MessageType:  "chose",
				MessageValue: peer.AcceptedValue,
				ProposalNum:  peer.AcceptedNum,
			})
			peer.hasChosen = true
		}
	}
}

func handleAcceptAck(peer *Peer, msg Message) {
	if !isProposer(peer.Roles) {
		return
	}

	if _, exists := peer.acceptResponses[msg.ProposalNum]; !exists {
		peer.acceptResponses[msg.ProposalNum] = make(map[int]Message)
	}
	peer.acceptResponses[msg.ProposalNum][msg.PeerID] = msg

	// Check if we have majority
	if len(peer.acceptResponses[msg.ProposalNum]) > peer.numAcceptors/2 {
		// Check for any rejections
		for _, response := range peer.acceptResponses[msg.ProposalNum] {
			if response.ProposalNum > msg.ProposalNum {
				// Got a rejection, need to start over with higher number
				cleanupResponses(peer, msg.ProposalNum) // Cleanup before retry
				startPaxos(peer)
				peer.mutex.Unlock()
				return
			}
		}

		// No rejections and we have majority - value is chosen
		logMessage(Message{
			PeerID:       peer.ID,
			Action:       "chose",
			MessageType:  "chose",
			MessageValue: msg.MessageValue,
			ProposalNum:  msg.ProposalNum,
		})

		// Cleanup after successful consensus
		cleanupResponses(peer, msg.ProposalNum)
	}
}

// Add this new helper function to count acceptors
func countAcceptors(hosts map[string][]string) int {
	count := 0
	for _, roles := range hosts {
		for _, role := range roles {
			if role == "acceptor1" { // Only count acceptors for proposer1 in this case
				count++
				break
			}
		}
	}
	return count
}

// Add cleanup function for response maps
func cleanupResponses(peer *Peer, proposalNum int) {
	// peer.mutex.Lock()
	// defer peer.mutex.Unlock()

	// Clean up old prepare responses
	for pn := range peer.prepareResponses {
		if pn < proposalNum {
			delete(peer.prepareResponses, pn)
		}
	}

	// Clean up old accept responses
	for pn := range peer.acceptResponses {
		if pn < proposalNum {
			delete(peer.acceptResponses, pn)
		}
	}
}

func isAcceptorFor(roles []string, proposerID int) bool {
	return containsRole(roles, fmt.Sprintf("acceptor%d", proposerID))
}
