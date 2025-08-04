#ifndef LIBMCPSPY_H
#define LIBMCPSPY_H

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/types.h>
#include <time.h>
#include <pthread.h>
#include <dlfcn.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>

// Configuration constants
#define MAX_BUF_SIZE (16 * 1024)
#define MAX_COMM_SIZE 16

// Transport types (simplified to stdio only)
typedef enum {
    TRANSPORT_STDIO = 1
} transport_type_t;

// Event types (simplified to read/write only)
typedef enum {
    EVENT_TYPE_READ = 1,
    EVENT_TYPE_WRITE = 2
} event_type_t;

// MCP event structure (simplified for stdio only)
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
} mcp_event_t;

// Configuration structure (simplified)
typedef struct {
    int monitor_stdio;
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

// System call hooks (LD_PRELOAD) - stdio only
ssize_t read(int fd, void *buf, size_t count);
ssize_t write(int fd, const void *buf, size_t count);

// Transport-specific monitoring (stdio only)
int stdio_monitor_init(void);
void stdio_monitor_cleanup(void);

// Utility functions
int is_stdio_fd(int fd);
const char* transport_type_to_string(transport_type_t type);
const char* event_type_to_string(event_type_t type);
void create_and_log_event(int fd, const void* buf, size_t size, event_type_t event_type, transport_type_t transport);

// JSON-RPC detection
int is_jsonrpc_message(const char* buf, size_t size);

// CGO interface functions (for Go integration)
int mcpspy_start_monitoring(const char* config_json);
int mcpspy_stop_monitoring(void);
int mcpspy_get_next_event(mcp_event_t* event, int timeout_ms);

#endif // LIBMCPSPY_H