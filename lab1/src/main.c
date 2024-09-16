#include "peer_program.h"

int main(int argc, char *argv[]) {
    const char *filename;

    // enable logging to a file
    log_set_level(LOG_ERROR);
    FILE *logfile = fopen("program.log", "w");
    if (logfile != NULL) {
        log_add_fp(logfile, LOG_INFO);
    } else {
        log_error("Failed to open log file");
    }

    // check command line argument for hosts file
    if (argc == 3 && strcmp(argv[1], "-h") == 0) {
        filename = argv[2];
    } else {
        log_error("Usage: %s -h <hostsfile.txt>", argv[0]);
        exit(1);
    }

    log_info("Using hosts file: %s", filename);

    // get hostname of current container
    char my_hostname[HOST_NAME_MAX];
    if (gethostname(my_hostname, HOST_NAME_MAX) != 0) {
        perror("gethostname");
        exit(1);
    }

    log_info("Starting program on %s", my_hostname);

    // read peers info from config file
    PeerInfo peer_info = read_config_file(filename);
    log_info("Read %d peers from config file", peer_info.n);

    // setup UDP listener socket
    int listener_fd = setup_listener_socket();
    if (listener_fd < 0) {
        log_error("Failed to set up listener socket");
        exit(1);
    }

    int ready_count = 0;

    // loop: discover peers until we've heard from all peers
    while (ready_count < peer_info.n - 1) {
        log_info("Sending heartbeats");
        send_heartbeat(listener_fd, &peer_info, my_hostname);

        // discover peers and increment ready count - only unique heartbeats from each peer are counted
        int discovered = discover_peers(listener_fd, &peer_info);
        log_info("discovered %d new peers", discovered);
        ready_count += discovered;

        // if we've discovered all peers - print READY and stop discovering peers
        if (ready_count >= peer_info.n - 1) {
            fprintf(stderr, "READY\n");
        }

        sleep(1);  // sleep for 1 second between attempts
    }

    // keep sending out heartbeats until program is terminated
    while (1) {
        send_heartbeat(listener_fd, &peer_info, my_hostname);
        log_info("Still alive in READY state");
        sleep(1);
    }

    // cleanup - handle sigint in the future
    cleanup(listener_fd);
    free_peer_info(&peer_info);
    if (logfile != NULL)
    {
        fclose(logfile);
    }
    return 0;
}

PeerInfo read_config_file(const char *filename) {
    PeerInfo info = {0};
    FILE *file = fopen(filename, "r");
    if (file == NULL) {
        log_error("Error opening file: %s", filename);
        exit(1);
    }

    char line[256];
    int capacity = 10;  // Initial capacity
    info.peers = malloc(capacity * sizeof(char*));
    info.ip_addresses = malloc(capacity * sizeof(char*));
    info.received = malloc(capacity * sizeof(int));

    if (!info.peers || !info.ip_addresses || !info.received) {
        log_error("Memory allocation failed");
        exit(1);
    }

    while (fgets(line, sizeof(line), file)) {
        size_t len = strlen(line);
        if (len > 0 && line[len-1] == '\n') {
            line[len-1] = '\0';
        }
        if (strlen(line) == 0) {
            continue;
        }

        // store the hostnames - if we run out of space, double the capacity
        if (info.n == capacity) {
            capacity *= 2;
            info.peers = realloc(info.peers, capacity * sizeof(char*));
            info.ip_addresses = realloc(info.ip_addresses, capacity * sizeof(char*));
            info.received = realloc(info.received, capacity * sizeof(int));
            if (!info.peers || !info.ip_addresses || !info.received) {
                log_error("Memory reallocation failed");
                exit(1);
            }
        }

        // initialize IP address as unknown
        info.peers[info.n] = strdup(line);
        info.ip_addresses[info.n] = NULL;
        info.received[info.n] = 0;
        info.n++;
    }

    fclose(file);
    return info;
}

void send_heartbeat(int socket_fd, PeerInfo *peer_info, const char *my_hostname) {
    struct addrinfo hints, *res;
    int status;
    char message[] = "HEARTBEAT";

    memset(&hints, 0, sizeof hints);
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_DGRAM;

    for (int i = 0; i < peer_info->n; i++) {
        // skip sending to self
        if (strcmp(peer_info->peers[i], my_hostname) == 0) {
            continue;
        }

        log_debug("Attempting to heartbeat to %s", peer_info->peers[i]);

        // resolve peer hostname to address info
        if ((status = getaddrinfo(peer_info->peers[i], PORT, &hints, &res)) != 0) {
            log_debug("getaddrinfo error for %s: %s", peer_info->peers[i], gai_strerror(status));
            continue;
        }

        // send heartbeat to the peer
        if (sendto(socket_fd, message, strlen(message), 0, res->ai_addr, res->ai_addrlen) == -1) {
            perror("sendto");
        } else {
            // if we successfully sent a heartbeat, update the IP address
            char ip[INET6_ADDRSTRLEN];
            void *addr;
            if (res->ai_family == AF_INET) {
                struct sockaddr_in *ipv4 = (struct sockaddr_in *)res->ai_addr;
                addr = &(ipv4->sin_addr);
            } else {
                struct sockaddr_in6 *ipv6 = (struct sockaddr_in6 *)res->ai_addr;
                addr = &(ipv6->sin6_addr);
            }
            inet_ntop(res->ai_family, addr, ip, sizeof ip);

            if (peer_info->ip_addresses[i] == NULL || strcmp(peer_info->ip_addresses[i], ip) != 0) {
                free(peer_info->ip_addresses[i]);
                peer_info->ip_addresses[i] = strdup(ip);
                log_debug("Updated IP for %s: %s", peer_info->peers[i], ip);
            }
        }

        freeaddrinfo(res);
    }
}

int discover_peers(int socket_fd, PeerInfo *peer_info)
{
    struct sockaddr_storage peer_addr;
    socklen_t addr_len = sizeof peer_addr;
    char buf[MAXBUF];

    log_debug("Attempting to receive heartbeats");

    // try to receive a message (non-blocking)
    int bytes_recv = recvfrom(socket_fd, buf, MAXBUF - 1, MSG_DONTWAIT,
                              (struct sockaddr *)&peer_addr, &addr_len);

    if (bytes_recv > 0) {
        buf[bytes_recv] = '\0';
        char host[INET6_ADDRSTRLEN];

        log_info("Received message of length %d", bytes_recv);

        // if the message is not a heartbeat, skip it
        if (strcmp(buf, "HEARTBEAT") != 0) {
            log_info("Received non-heartbeat message: %s", buf);
            return 0;
        }

        // convert sender's address to string
        int status = getnameinfo((struct sockaddr *)&peer_addr, addr_len, host, sizeof(host), NULL, 0, NI_NUMERICHOST);
        if (status == 0) {
            log_info("Sender IP: %s", host);

            if (peer_info == NULL) {
                log_error("Error: peer_info is NULL");
                return 0;
            }

            int peer_index = get_peer_index(peer_info, host);
            log_info("Peer index: %d", peer_index);

            // count unique messages from each peer
            if (peer_index != -1) {
                if (peer_index >= 0 && peer_index < peer_info->n) {
                    if (!peer_info->received[peer_index]) {
                        log_info("Received heartbeat from %s (%s): %s", peer_info->peers[peer_index], host, buf);
                        peer_info->received[peer_index] = 1;
                        return 1;
                    } else {
                        log_info("Received duplicate heartbeat from %s (%s)", peer_info->peers[peer_index], host);
                    }
                } else {
                    log_error("Error: peer_index %d out of bounds (0-%d)", peer_index, peer_info->n - 1);
                }
            } else {
                log_info("Received heartbeat from unknown peer: %s", host);
            }
        } else {
            log_error("getnameinfo error: %s", gai_strerror(status));
        }
    } else if (bytes_recv == 0) {
        log_info("Received empty message");
    } else if (bytes_recv == -1) {
        if (errno == EAGAIN || errno == EWOULDBLOCK) {
            log_info("No message available");
        } else {
            perror("recvfrom");
        }
    }

    return 0; // No new peers discovered
}

// find the index of a peer given its IP address
int get_peer_index(PeerInfo *peer_info, const char *ip_address) {
    log_debug("searching for peer with IP: %s", ip_address);
    for (int i = 0; i < peer_info->n; i++) {
        log_trace("Comparing with peer %d: %s", i, peer_info->ip_addresses[i]);
        if (peer_info->ip_addresses[i] != NULL && strcmp(peer_info->ip_addresses[i], ip_address) == 0) {
            log_debug("Found matching peer at index %d", i);
            return i;
        }
    }
    log_debug("No matching peer found for IP: %s", ip_address);
    return -1; // Peer not found
}

void free_peer_info(PeerInfo *info) {
    for (int i = 0; i < info->n; i++) {
        free(info->peers[i]);
        if (info->ip_addresses[i] != NULL) {
            free(info->ip_addresses[i]);
        }
    }
    free(info->peers);
    free(info->ip_addresses);
    free(info->received);
    info->n = 0;
}

int setup_listener_socket() {
    struct addrinfo hints, *res, *p;
    int listener_fd;
    int status;

    memset(&hints, 0, sizeof hints);
    hints.ai_family = AF_UNSPEC;     // don't care IPv4 or IPv6
    hints.ai_socktype = SOCK_DGRAM;  // UDP datagram sockets
    hints.ai_flags = AI_PASSIVE;     // fill in my IP for me

    if ((status = getaddrinfo(NULL, PORT, &hints, &res)) != 0) {
        log_error("getaddrinfo error: %s", gai_strerror(status));
        exit(1);
    }

    // loop through all the results and bind to the first we can
    for (p = res; p != NULL; p = p->ai_next) {
        if ((listener_fd = socket(p->ai_family, p->ai_socktype, p->ai_protocol)) == -1) {
            perror("server: socket");
            continue;
        }

        if (bind(listener_fd, p->ai_addr, p->ai_addrlen) == -1) {
            close(listener_fd);
            perror("server: bind");
            continue;
        }

        break;
    }

    if (p == NULL) {
        log_error("Failed to bind socket");
        exit(1);
    }

    freeaddrinfo(res);
    return listener_fd;
}

void cleanup(int socket_fd) {
    // close the socket
    close(socket_fd);
}
