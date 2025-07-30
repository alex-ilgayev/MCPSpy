#ifndef LIBMCPSPY_H
#define LIBMCPSPY_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <time.h>
#include <pthread.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <stdarg.h>
#include <signal.h>

// Configuration constants
#define MAX_BUF_SIZE (16 * 1024)
#define MAX_PATH_SIZE 512
#define MAX_COMM_SIZE 16
#define MAX_LOG_LINE 4096

// Transport types
typedef enum {
    TRANSPORT_STDIO = 1,
    TRANSPORT_HTTP = 2,
    TRANSPORT_HTTPS = 3,
    TRANSPORT_SOCKET = 4,
    TRANSPORT_PACKET = 5
} transport_type_t;

// Event types  
typedef enum {
    EVENT_TYPE_READ = 1,
    EVENT_TYPE_WRITE = 2,
    EVENT_TYPE_CONNECT = 3,
    EVENT_TYPE_ACCEPT = 4,
    EVENT_TYPE_CLOSE = 5
} event_type_t;

// MCP event structure (matches eBPF event structure)
typedef struct {
    time_t timestamp;
    pid_t pid;
    char comm[MAX_COMM_SIZE];
    transport_type_t transport;
    event_type_t event_type;
    int fd;
    size_t size;
    size_t buf_size;
    char buf[MAX_BUF_SIZE];
    char remote_addr[INET_ADDRSTRLEN];
    int remote_port;
} mcp_event_t;

// Configuration structure
typedef struct {
    int monitor_stdio;
    int monitor_http;  
    int monitor_https;
    int monitor_sockets;
    int monitor_packets;
    char log_file[MAX_PATH_SIZE];
    int log_level;
    int enable_ssl_mitm;
    char cert_file[MAX_PATH_SIZE];
    char key_file[MAX_PATH_SIZE];
} mcpspy_config_t;

// Global configuration
extern mcpspy_config_t g_config;
extern int g_initialized;
extern pthread_mutex_t g_log_mutex;
extern FILE* g_log_file;

// Core functions
int mcpspy_init(const mcpspy_config_t* config);
void mcpspy_cleanup(void);
int mcpspy_is_mcp_data(const char* buf, size_t size);
void mcpspy_log_event(const mcp_event_t* event);

// System call hooks (LD_PRELOAD)
ssize_t read(int fd, void *buf, size_t count);
ssize_t write(int fd, const void *buf, size_t count);
ssize_t send(int sockfd, const void *buf, size_t len, int flags);
ssize_t recv(int sockfd, void *buf, size_t len, int flags);
int connect(int sockfd, const struct sockaddr *addr, socklen_t addrlen);
int accept(int sockfd, struct sockaddr *addr, socklen_t *addrlen);
int accept4(int sockfd, struct sockaddr *addr, socklen_t *addrlen, int flags);
int close(int fd);

// Transport-specific monitoring
int stdio_monitor_init(void);
int http_monitor_init(void);
int ssl_monitor_init(void);
int packet_monitor_init(void);

void stdio_monitor_cleanup(void);
void http_monitor_cleanup(void);
void ssl_monitor_cleanup(void);
void packet_monitor_cleanup(void);

// Utility functions
int is_stdio_fd(int fd);
int is_socket_fd(int fd);
int is_http_port(int port);
int is_https_port(int port);
void get_socket_info(int sockfd, char* addr_buf, int* port);
const char* transport_type_to_string(transport_type_t type);
const char* event_type_to_string(event_type_t type);
void create_and_log_event(int fd, const void* buf, size_t size, event_type_t event_type, transport_type_t transport);

// JSON-RPC detection
int is_jsonrpc_message(const char* buf, size_t size);
int extract_jsonrpc_method(const char* buf, size_t size, char* method, size_t method_size);

// Thread safety
void mcpspy_lock(void);
void mcpspy_unlock(void);

// Environment variable handling
void mcpspy_load_config_from_env(mcpspy_config_t* config);

// CGO interface functions (for Go integration)
int mcpspy_start_monitoring(const char* config_json);
int mcpspy_stop_monitoring(void);
int mcpspy_get_next_event(mcp_event_t* event, int timeout_ms);

#endif // LIBMCPSPY_H