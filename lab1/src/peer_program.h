#ifndef PEER_PROGRAM_H
#define PEER_PROGRAM_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <arpa/inet.h>
#include <errno.h>
#include <netdb.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <signal.h>
#include "lib/log.h"

#define MAXBUF 1024
#define PORT "8080"
#define HOST_NAME_MAX 256

// structure to hold information about peers
typedef struct
{
    int n;                         // number of peers
    char **peers;                  // array of peer hostnames
    char **ip_addresses;           // array of peer IP addresses
    int *received;                 // array to track received messages from peers
} PeerInfo;

// function prototypes
PeerInfo read_config_file(const char *filename);
int setup_listener_socket();
void send_heartbeat(int socket_fd, PeerInfo *peer_info, const char *my_hostname);
int discover_peers(int socket_fd, PeerInfo *peer_info);
void cleanup(int socket_fd);
void free_peer_info(PeerInfo *info);
int get_peer_index(PeerInfo *peer_info, const char *ip_address);

#endif // PEER_PROGRAM_H
