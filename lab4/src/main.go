package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
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
	ProposerNum      int
	CurrentProposal  int
	Roles            []string
	MinProposal      int
	AcceptedValue    string
	AcceptedNum      int
	ProposedValue    string
	Connections      map[string]net.Conn
	mutex            sync.Mutex
	prepareResponses map[int]map[int]Message
	acceptResponses  map[int]map[int]Message
	numAcceptors     int
	PeerRoles        map[string][]string
	hasChosen        bool
	acceptSent       map[int]bool
}

func main() {
	// Parse command line arguments
	hostsFile := flag.String("h", "", "Path to hosts file")
	value := flag.String("v", "", "Proposed value")
	delay := flag.Float64("t", 0, "Delay for secondary proposer (in seconds)")
	flag.Parse()

	if *hostsFile == "" {
		fmt.Fprintf(os.Stderr, "Hosts file is required\n")
		os.Exit(1)
	}

	// Read and parse hosts file
	hosts, err := parseHostsFile(*hostsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing hosts file: %v\n", err)
		os.Exit(1)
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting hostname: %v\n", err)
		os.Exit(1)
	}

	// Initialize peer
	proposerNum := getProposerNumber(hosts[hostname])
	peer := &Peer{
		ID:               getPeerID(hostname, hosts),
		ProposerNum:      proposerNum,
		CurrentProposal:  proposerNum - 1,
		Roles:            hosts[hostname],
		Connections:      make(map[string]net.Conn),
		prepareResponses: make(map[int]map[int]Message),
		acceptResponses:  make(map[int]map[int]Message),
		numAcceptors:     countAcceptors(hosts, proposerNum),
		PeerRoles:        hosts,
		acceptSent:       make(map[int]bool),
	}

	// Start server - listen for incoming messages
	go startServer(peer)

	// If this peer is a proposer - start Paxos
	if isProposer(peer.Roles) {
		// If value flag is provided, set it as the proposed value
		if *value != "" {
			peer.ProposedValue = *value
		}

		// Apply delay if specified
		if *delay > 0 {
			time.Sleep(time.Duration(*delay * float64(time.Second)))
		}
		startPaxos(peer)
	}

	// Keep the program running
	select {}
}

// Check if this peer is a proposer
func isProposer(roles []string) bool {
	for _, role := range roles {
		if strings.HasPrefix(role, "proposer") {
			return true
		}
	}
	return false
}

// Parse the hosts file
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
		fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
		os.Exit(1)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accepting connection: %v\n", err)
			continue
		}
		go handleConnection(peer, conn)
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
	for hostname, roles := range peer.PeerRoles {
		// Skip if it's the current peer
		if getPeerID(hostname, peer.PeerRoles) == peer.ID {
			continue
		}
		// Use proposer number instead of peer ID
		if isAcceptorFor(roles, peer.ProposerNum) {
			sendMessage(peer, hostname, msg)
		}
	}
}

func sendMessage(peer *Peer, hostname string, msg Message) error {
	conn, err := establishConnection(peer, hostname)
	if err != nil {
		return err
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = conn.Write(msgBytes)
	return err
}

func logMessage(msg Message) {
	jsonMsg, _ := json.Marshal(msg)
	fmt.Fprintln(os.Stderr, string(jsonMsg))
}

func startPaxos(peer *Peer) {
	// Increment proposal number for new round
	peer.mutex.Lock()
	peer.CurrentProposal++
	proposalNum := peer.CurrentProposal
	peer.hasChosen = false // Reset hasChosen to allow new 'chose' message for this round
	peer.mutex.Unlock()

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
		PeerID:       msg.PeerID,
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
	// Find the proposer number from the sender's roles
	// This is needed because acceptors are named acceptor1/acceptor2, not by peer ID
	var proposerNum int
	for hostname, roles := range peer.PeerRoles {
		if getPeerID(hostname, peer.PeerRoles) == msg.PeerID {
			proposerNum = getProposerNumber(roles)
			break
		}
	}

	// Check if we're an acceptor for this proposer number
	if !isAcceptorFor(peer.Roles, proposerNum) {
		return
	}

	// If this is a higher proposal number, we must accept it
	if msg.ProposalNum > peer.MinProposal {
		peer.MinProposal = msg.ProposalNum
		peer.hasChosen = false // Reset hasChosen to allow new 'chose' message for higher proposal

		// If we have previously accepted a value, include it in response
		responseNum := msg.ProposalNum
		if peer.AcceptedValue != "" {
			responseNum = peer.AcceptedNum
		}

		// Prepare response with any previously accepted value
		response := Message{
			PeerID:       peer.ID,
			Action:       "sent",
			MessageType:  "prepare_ack",
			MessageValue: peer.AcceptedValue, // Include any previously accepted value
			ProposalNum:  responseNum,        // Include the proposal number of accepted value
		}

		logMessage(response)

		// Find and send response to the original proposer
		for hostname, roles := range peer.PeerRoles {
			if containsRole(roles, fmt.Sprintf("proposer%d", proposerNum)) {
				sendMessage(peer, hostname, response)
				break
			}
		}
	}
}

func handlePrepareAck(peer *Peer, msg Message) {
	if !isProposer(peer.Roles) {
		return
	}

	// Skip if we've already sent accept for this proposal
	if peer.acceptSent[msg.ProposalNum] {
		return
	}

	// Store the prepare_ack response
	if _, exists := peer.prepareResponses[msg.ProposalNum]; !exists {
		peer.prepareResponses[msg.ProposalNum] = make(map[int]Message)
	}
	peer.prepareResponses[msg.ProposalNum][msg.PeerID] = msg

	// Check if we have majority of responses
	if len(peer.prepareResponses[msg.ProposalNum]) > peer.numAcceptors/2 {
		// Find highest accepted proposal among responses
		highestAcceptedNum := 0
		var valueToPropose string = peer.ProposedValue

		// If any acceptor has already accepted a value, use the value
		// with the highest proposal number (Paxos requirement)
		for _, response := range peer.prepareResponses[msg.ProposalNum] {
			if response.ProposalNum > highestAcceptedNum && response.MessageValue != "" {
				highestAcceptedNum = response.ProposalNum
				valueToPropose = response.MessageValue
			}
		}

		// Mark that we've sent accept for this proposal to avoid duplicates
		peer.acceptSent[msg.ProposalNum] = true

		// Send accept message with either the highest accepted value
		// or our proposed value if no value was previously accepted
		acceptMsg := Message{
			PeerID:       peer.ID,
			Action:       "sent",
			MessageType:  "accept",
			MessageValue: valueToPropose,
			ProposalNum:  peer.CurrentProposal,
		}

		logMessage(acceptMsg)
		broadcastToAcceptors(peer, acceptMsg)
	}
}

func handleAccept(peer *Peer, msg Message) {
	// Find the proposer number from the sender's roles
	var proposerNum int
	for hostname, roles := range peer.PeerRoles {
		if getPeerID(hostname, peer.PeerRoles) == msg.PeerID {
			proposerNum = getProposerNumber(roles)
			break
		}
	}

	// Check if we're an acceptor for this proposer number
	if !isAcceptorFor(peer.Roles, proposerNum) {
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
		for hostname := range peer.Connections {
			if containsRole(peer.PeerRoles[hostname], fmt.Sprintf("proposer%d", proposerNum)) {
				sendMessage(peer, hostname, response)
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
				peer.mutex.Unlock()
				startPaxos(peer)
				return
			}
		}

		// No rejections and we have majority - value is chosen
		if !peer.hasChosen {
			logMessage(Message{
				PeerID:       peer.ID,
				Action:       "chose",
				MessageType:  "chose",
				MessageValue: msg.MessageValue,
				ProposalNum:  msg.ProposalNum,
			})
			peer.hasChosen = true
		}

		// Cleanup after successful consensus
		cleanupResponses(peer, msg.ProposalNum)
	}
}

// Add this new helper function to count acceptors
func countAcceptors(hosts map[string][]string, proposerID int) int {
	count := 0
	acceptorRole := fmt.Sprintf("acceptor%d", proposerID)

	for _, roles := range hosts {
		if containsRole(roles, acceptorRole) {
			count++
		}
	}

	return count
}

// Add cleanup function for response maps
func cleanupResponses(peer *Peer, proposalNum int) {
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

	// Clean up old acceptSent flags
	for pn := range peer.acceptSent {
		if pn < proposalNum {
			delete(peer.acceptSent, pn)
		}
	}
}

func isAcceptorFor(roles []string, proposerID int) bool {
	return containsRole(roles, fmt.Sprintf("acceptor%d", proposerID))
}

// Add this helper function to establish a single connection
func establishConnection(peer *Peer, hostname string) (net.Conn, error) {

	// Check if connection already exists
	if conn, exists := peer.Connections[hostname]; exists {
		return conn, nil
	}

	// Try to establish new connection
	conn, err := net.Dial("tcp", hostname+":"+PORT)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %v", hostname, err)
	}

	// Store the new connection
	peer.Connections[hostname] = conn
	return conn, nil
}

// Add helper function to get proposer number
func getProposerNumber(roles []string) int {
	for _, role := range roles {
		if strings.HasPrefix(role, "proposer") {
			num, _ := strconv.Atoi(strings.TrimPrefix(role, "proposer"))
			return num
		}
	}
	return -1
}
