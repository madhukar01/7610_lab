package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Process represents a node in the distributed system
type Process struct {
	ID          int
	State       int
	Predecessor int
	Successor   int
	Connections map[int]net.Conn
	HasToken    bool
	mutex       sync.Mutex
	sleepTime   time.Duration
}

// printInfo outputs the process information to stderr
func (p *Process) printInfo() {
	fmt.Fprintf(os.Stderr, "{proc_id: %d, state: %d, predecessor: %d, successor: %d}\n", p.ID, p.State, p.Predecessor, p.Successor)
}

// printState outputs the current state of the process to stderr
func (p *Process) printState() {
	fmt.Fprintf(os.Stderr, "{proc_id: %d, state: %d}\n", p.ID, p.State)
}

// printTokenPass outputs information about token passing to stderr
func (p *Process) printTokenPass(sender, receiver int) {
	fmt.Fprintf(os.Stderr, "{proc_id: %d, sender: %d, receiver: %d, message:\"token\"}\n", p.ID, sender, receiver)
}

// sendToken attempts to send the token to the successor process
func (p *Process) sendToken() {
	p.mutex.Lock()
	p.HasToken = false
	p.mutex.Unlock()

	for {
		p.mutex.Lock()
		conn, ok := p.Connections[p.Successor]
		p.mutex.Unlock()

		if ok {
			p.printTokenPass(p.ID, p.Successor)
			_, err := conn.Write([]byte("TOKEN"))
			if err == nil {
				return // Token sent successfully
			}
		}
		time.Sleep(time.Second) // Wait before retrying
	}
}

// processToken handles receiving a token, updating state, and passing it on
func (p *Process) processToken() {
	p.mutex.Lock()
	p.State++
	p.printState()
	p.mutex.Unlock()

	time.Sleep(p.sleepTime)
	p.sendToken()
}

func main() {
	// Configure logging to only show fatal errors on stderr
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("")

	// Parse command-line flags
	hostsFile := flag.String("h", "", "Path to the hostsfile")
	sleepTime := flag.Float64("t", 0.0, "Sleep time in seconds")
	hasInitialToken := flag.Bool("x", false, "Whether this process starts with the token")
	flag.Parse()

	// Validate command-line arguments
	if *hostsFile == "" || *sleepTime == 0.0 {
		log.Fatal("Usage: go run main.go -h <hostsfile> -t <sleep_time> [-x]\n")
	}

	// Read hosts from the hostsfile
	hosts, err := readHostsFile(*hostsFile)
	if err != nil {
		log.Fatal("Error reading hostsfile: ", err, "\n")
	}

	// Get the hostname of the current process
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal("Error getting hostname: ", err, "\n")
	}

	// Determine the process ID based on the hostname's position in the hostsfile
	processID := 0
	for i, host := range hosts {
		if host == hostname {
			processID = i + 1
			break
		}
	}

	if processID == 0 {
		log.Fatal("hostname not found in hostsfile\n")
	}

	// Initialize the process
	process := &Process{
		ID:        processID,
		State:     0,
		HasToken:  *hasInitialToken,
		sleepTime: time.Duration(*sleepTime * float64(time.Second)),
	}

	// Set predecessor and successor IDs
	process.Predecessor = ((processID - 2 + len(hosts)) % len(hosts)) + 1
	process.Successor = (processID % len(hosts)) + 1

	process.printInfo()

	// Start listening for incoming connections
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("Error starting listener: ", err, "\n")
	}

	// Set up connections to other processes
	ready := make(chan bool)
	process.Connections = setupConnections(hosts, processID, ready)

	// Handle incoming connections - runs in a new goroutine
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				continue
			}
			go handleConnection(conn, process)
		}
	}()

	// Wait for all connections to be established
	<-ready

	// If this process starts with the token, begin token passing
	if process.HasToken {
		process.sendToken()
	}

	// Keep the main goroutine running
	select {}
}

// setupConnections establishes connections with other processes
func setupConnections(hosts []string, myID int, ready chan<- bool) map[int]net.Conn {
	connections := make(map[int]net.Conn)
	var mutex sync.Mutex
	var wg sync.WaitGroup

	for i, host := range hosts {
		if i+1 != myID {
			wg.Add(1)
			go func(id int, host string) {
				defer wg.Done()
				for {
					conn, err := net.Dial("tcp", host+":8080")
					if err == nil {
						mutex.Lock()
						connections[id] = conn
						mutex.Unlock()
						return
					}
					time.Sleep(time.Second) // Wait before retrying
				}
			}(i+1, host)
		}
	}

	// Signal when all connections are established
	go func() {
		wg.Wait()
		ready <- true
	}()

	return connections
}

// handleConnection processes incoming messages on a connection
func handleConnection(conn net.Conn, process *Process) {
	defer conn.Close()
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			return // Silently handle connection errors
		}
		message := string(buffer[:n])
		if message == "TOKEN" {
			process.processToken()
		}
	}
}

// readHostsFile reads and returns the list of hosts from the given file
func readHostsFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hosts []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		hosts = append(hosts, strings.TrimSpace(scanner.Text()))
	}
	return hosts, scanner.Err()
}
