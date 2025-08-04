#define _GNU_SOURCE
#include "libmcpspy.h"
#include <sys/syscall.h>
#ifdef __linux__
#include <linux/limits.h>
#else
#include <limits.h>
#endif

// Global state
mcpspy_config_t g_config = {0};
int g_initialized = 0;
pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;
FILE* g_log_file = NULL;

// Original system call function pointers
static ssize_t (*original_read)(int fd, void *buf, size_t count) = NULL;
static ssize_t (*original_write)(int fd, const void *buf, size_t count) = NULL;
static ssize_t (*original_send)(int sockfd, const void *buf, size_t len, int flags) = NULL;
static ssize_t (*original_recv)(int sockfd, void *buf, size_t len, int flags) = NULL;
static int (*original_connect)(int sockfd, const struct sockaddr *addr, socklen_t addrlen) = NULL;
static int (*original_accept)(int sockfd, struct sockaddr *addr, socklen_t *addrlen) = NULL;
static int (*original_accept4)(int sockfd, struct sockaddr *addr, socklen_t *addrlen, int flags) = NULL;
static int (*original_close)(int fd) = NULL;

// Event queue for CGO interface
#define EVENT_QUEUE_SIZE 1000
static mcp_event_t event_queue[EVENT_QUEUE_SIZE];
static int queue_head = 0;
static int queue_tail = 0;
static pthread_mutex_t queue_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t queue_cond = PTHREAD_COND_INITIALIZER;

// Load original system call functions
static void load_original_functions(void) {
    if (original_read == NULL) {
        original_read = (ssize_t (*)(int, void *, size_t))dlsym(RTLD_NEXT, "read");
    }
    if (original_write == NULL) {
        original_write = (ssize_t (*)(int, const void *, size_t))dlsym(RTLD_NEXT, "write");
    }
    if (original_send == NULL) {
        original_send = (ssize_t (*)(int, const void *, size_t, int))dlsym(RTLD_NEXT, "send");
    }
    if (original_recv == NULL) {
        original_recv = (ssize_t (*)(int, void *, size_t, int))dlsym(RTLD_NEXT, "recv");
    }
    if (original_connect == NULL) {
        original_connect = (int (*)(int, const struct sockaddr *, socklen_t))dlsym(RTLD_NEXT, "connect");
    }
    if (original_accept == NULL) {
        original_accept = (int (*)(int, struct sockaddr *, socklen_t *))dlsym(RTLD_NEXT, "accept");
    }
    if (original_accept4 == NULL) {
        original_accept4 = (int (*)(int, struct sockaddr *, socklen_t *, int))dlsym(RTLD_NEXT, "accept4");
    }
    if (original_close == NULL) {
        original_close = (int (*)(int))dlsym(RTLD_NEXT, "close");
    }
}

// Initialize MCPSpy monitoring
int mcpspy_init(const mcpspy_config_t* config) {
    if (g_initialized) {
        return 0; // Already initialized
    }

    // Load configuration
    if (config) {
        memcpy(&g_config, config, sizeof(mcpspy_config_t));
    } else {
        // Load from environment variables
        mcpspy_load_config_from_env(&g_config);
    }

    // Load original function pointers
    load_original_functions();

    // Open log file if specified
    if (strlen(g_config.log_file) > 0) {
        g_log_file = fopen(g_config.log_file, "a");
        if (!g_log_file) {
            fprintf(stderr, "mcpspy: Failed to open log file: %s\n", g_config.log_file);
            return -1;
        }
    }

    // Initialize transport-specific monitoring
    if (g_config.monitor_stdio && stdio_monitor_init() != 0) {
        fprintf(stderr, "mcpspy: Failed to initialize stdio monitoring\n");
        return -1;
    }

    g_initialized = 1;
    return 0;
}

// Cleanup MCPSpy monitoring
void mcpspy_cleanup(void) {
    if (!g_initialized) {
        return;
    }

    // Cleanup transport-specific monitoring
    if (g_config.monitor_stdio) {
        stdio_monitor_cleanup();
    }

    // Close log file
    if (g_log_file && g_log_file != stdout && g_log_file != stderr) {
        fclose(g_log_file);
        g_log_file = NULL;
    }

    g_initialized = 0;
}

// Check if data looks like MCP JSON-RPC (similar to eBPF version)
int mcpspy_is_mcp_data(const char* buf, size_t size) {
    if (size < 1 || !buf) {
        return 0;
    }

    // Skip whitespace and look for '{'
    for (size_t i = 0; i < size && i < 8; i++) {
        char c = buf[i];
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
            continue;
        }
        if (c == '{') {
            return 1;
        }
        break;
    }
    return 0;
}

// Enhanced JSON-RPC detection
int is_jsonrpc_message(const char* buf, size_t size) {
    if (!mcpspy_is_mcp_data(buf, size)) {
        return 0;
    }

    // Look for JSON-RPC 2.0 indicators
    if (size > 20) {
        if (strstr(buf, "\"jsonrpc\"") && strstr(buf, "\"2.0\"")) {
            return 1;
        }
        if (strstr(buf, "\"method\"") || strstr(buf, "\"result\"") || strstr(buf, "\"error\"")) {
            return 1;
        }
    }

    return 0;
}

// Queue event for CGO interface
static void queue_event(const mcp_event_t* event) {
    pthread_mutex_lock(&queue_mutex);
    
    int next_tail = (queue_tail + 1) % EVENT_QUEUE_SIZE;
    if (next_tail != queue_head) {
        memcpy(&event_queue[queue_tail], event, sizeof(mcp_event_t));
        queue_tail = next_tail;
        pthread_cond_signal(&queue_cond);
    }
    
    pthread_mutex_unlock(&queue_mutex);
}

// Log MCP event
void mcpspy_log_event(const mcp_event_t* event) {
    if (!g_initialized || !event) {
        return;
    }

    pthread_mutex_lock(&g_log_mutex);

    FILE* output = g_log_file ? g_log_file : stdout;

    // JSONL format output (similar to eBPF version)
    fprintf(output, "{\"timestamp\":\"%ld\",\"pid\":%d,\"comm\":\"%s\",\"transport\":\"%s\",\"event_type\":\"%s\",\"fd\":%d,\"size\":%zu",
            event->timestamp, event->pid, event->comm,
            transport_type_to_string(event->transport),
            event_type_to_string(event->event_type),
            event->fd, event->size);

    if (event->buf_size > 0) {
        fprintf(output, ",\"data\":\"");
        for (size_t i = 0; i < event->buf_size && i < 256; i++) {
            char c = event->buf[i];
            if (c == '"' || c == '\\') {
                fprintf(output, "\\%c", c);
            } else if (c >= 32 && c <= 126) {
                fprintf(output, "%c", c);
            } else {
                fprintf(output, "\\u%04x", (unsigned char)c);
            }
        }
        fprintf(output, "\"");
    }

    if (strlen(event->remote_addr) > 0) {
        fprintf(output, ",\"remote_addr\":\"%s\",\"remote_port\":%d", event->remote_addr, event->remote_port);
    }

    fprintf(output, "}\n");
    fflush(output);

    pthread_mutex_unlock(&g_log_mutex);

    // Also queue for CGO interface
    queue_event(event);
}

// Create and log event
void create_and_log_event(int fd, const void* buf, size_t size, event_type_t event_type, transport_type_t transport) {
    if (!g_initialized || !is_jsonrpc_message((const char*)buf, size)) {
        return;
    }

    mcp_event_t event = {0};
    event.timestamp = time(NULL);
    event.pid = getpid();
    event.fd = fd;
    event.size = size;
    event.buf_size = size < MAX_BUF_SIZE ? size : MAX_BUF_SIZE;
    event.event_type = event_type;
    event.transport = transport;

    // Get process name
    char proc_path[64];
    snprintf(proc_path, sizeof(proc_path), "/proc/%d/comm", event.pid);
    FILE* comm_file = fopen(proc_path, "r");
    if (comm_file) {
        if (fgets(event.comm, sizeof(event.comm), comm_file)) {
            // Remove newline
            char* newline = strchr(event.comm, '\n');
            if (newline) *newline = '\0';
        }
        fclose(comm_file);
    }

    // Copy buffer data
    if (buf && event.buf_size > 0) {
        memcpy(event.buf, buf, event.buf_size);
    }

    // Get socket info if applicable
    if (is_socket_fd(fd)) {
        get_socket_info(fd, event.remote_addr, &event.remote_port);
    }

    mcpspy_log_event(&event);
}

// LD_PRELOAD hooked functions
ssize_t read(int fd, void *buf, size_t count) {
    if (!original_read) {
        load_original_functions();
    }

    ssize_t result = original_read(fd, buf, count);
    
    if (result > 0 && g_initialized) {
        transport_type_t transport = is_stdio_fd(fd) ? TRANSPORT_STDIO : 
                                   is_socket_fd(fd) ? TRANSPORT_SOCKET : TRANSPORT_STDIO;
        
        if ((transport == TRANSPORT_STDIO && g_config.monitor_stdio) ||
            (transport == TRANSPORT_SOCKET && g_config.monitor_sockets)) {
            create_and_log_event(fd, buf, result, EVENT_TYPE_READ, transport);
        }
    }

    return result;
}

ssize_t write(int fd, const void *buf, size_t count) {
    if (!original_write) {
        load_original_functions();
    }

    ssize_t result = original_write(fd, buf, count);
    
    if (result > 0 && g_initialized) {
        transport_type_t transport = is_stdio_fd(fd) ? TRANSPORT_STDIO : 
                                   is_socket_fd(fd) ? TRANSPORT_SOCKET : TRANSPORT_STDIO;
        
        if ((transport == TRANSPORT_STDIO && g_config.monitor_stdio) ||
            (transport == TRANSPORT_SOCKET && g_config.monitor_sockets)) {
            create_and_log_event(fd, buf, result, EVENT_TYPE_WRITE, transport);
        }
    }

    return result;
}

ssize_t send(int sockfd, const void *buf, size_t len, int flags) {
    if (!original_send) {
        load_original_functions();
    }

    ssize_t result = original_send(sockfd, buf, len, flags);
    
    if (result > 0 && g_initialized && g_config.monitor_sockets) {
        create_and_log_event(sockfd, buf, result, EVENT_TYPE_WRITE, TRANSPORT_SOCKET);
    }

    return result;
}

ssize_t recv(int sockfd, void *buf, size_t len, int flags) {
    if (!original_recv) {
        load_original_functions();
    }

    ssize_t result = original_recv(sockfd, buf, len, flags);
    
    if (result > 0 && g_initialized && g_config.monitor_sockets) {
        create_and_log_event(sockfd, buf, result, EVENT_TYPE_READ, TRANSPORT_SOCKET);
    }

    return result;
}

int connect(int sockfd, const struct sockaddr *addr, socklen_t addrlen) {
    if (!original_connect) {
        load_original_functions();
    }

    int result = original_connect(sockfd, addr, addrlen);
    
    if (result == 0 && g_initialized && g_config.monitor_sockets) {
        create_and_log_event(sockfd, NULL, 0, EVENT_TYPE_CONNECT, TRANSPORT_SOCKET);
    }

    return result;
}

int accept(int sockfd, struct sockaddr *addr, socklen_t *addrlen) {
    if (!original_accept) {
        load_original_functions();
    }

    int result = original_accept(sockfd, addr, addrlen);
    
    if (result >= 0 && g_initialized && g_config.monitor_sockets) {
        create_and_log_event(result, NULL, 0, EVENT_TYPE_ACCEPT, TRANSPORT_SOCKET);
    }

    return result;
}

int accept4(int sockfd, struct sockaddr *addr, socklen_t *addrlen, int flags) {
    if (!original_accept4) {
        load_original_functions();
    }

    int result = original_accept4(sockfd, addr, addrlen, flags);
    
    if (result >= 0 && g_initialized && g_config.monitor_sockets) {
        create_and_log_event(result, NULL, 0, EVENT_TYPE_ACCEPT, TRANSPORT_SOCKET);
    }

    return result;
}

int close(int fd) {
    if (!original_close) {
        load_original_functions();
    }

    if (g_initialized && (is_stdio_fd(fd) || is_socket_fd(fd))) {
        transport_type_t transport = is_stdio_fd(fd) ? TRANSPORT_STDIO : TRANSPORT_SOCKET;
        if ((transport == TRANSPORT_STDIO && g_config.monitor_stdio) ||
            (transport == TRANSPORT_SOCKET && g_config.monitor_sockets)) {
            create_and_log_event(fd, NULL, 0, EVENT_TYPE_CLOSE, transport);
        }
    }

    return original_close(fd);
}

// Utility functions
int is_stdio_fd(int fd) {
    return fd == STDIN_FILENO || fd == STDOUT_FILENO || fd == STDERR_FILENO;
}

int is_socket_fd(int fd) {
    struct stat stat_buf;
    if (fstat(fd, &stat_buf) == 0) {
        return S_ISSOCK(stat_buf.st_mode);
    }
    return 0;
}

void get_socket_info(int sockfd, char* addr_buf, int* port) {
    struct sockaddr_in addr;
    socklen_t addr_len = sizeof(addr);
    
    if (getpeername(sockfd, (struct sockaddr*)&addr, &addr_len) == 0) {
        inet_ntop(AF_INET, &addr.sin_addr, addr_buf, INET_ADDRSTRLEN);
        *port = ntohs(addr.sin_port);
    }
}

const char* transport_type_to_string(transport_type_t type) {
    switch (type) {
        case TRANSPORT_STDIO: return "stdio";
        case TRANSPORT_HTTP: return "http";
        case TRANSPORT_HTTPS: return "https";
        case TRANSPORT_SOCKET: return "socket";
        case TRANSPORT_PACKET: return "packet";
        default: return "unknown";
    }
}

const char* event_type_to_string(event_type_t type) {
    switch (type) {
        case EVENT_TYPE_READ: return "read";
        case EVENT_TYPE_WRITE: return "write";
        case EVENT_TYPE_CONNECT: return "connect";
        case EVENT_TYPE_ACCEPT: return "accept";
        case EVENT_TYPE_CLOSE: return "close";
        default: return "unknown";
    }
}

// Load configuration from environment variables
void mcpspy_load_config_from_env(mcpspy_config_t* config) {
    // Set defaults
    config->monitor_stdio = 1;
    config->monitor_http = 1;
    config->monitor_https = 1;
    config->monitor_sockets = 1;
    config->monitor_packets = 0; // Disabled by default
    config->log_level = 1;
    config->enable_ssl_mitm = 0;
    strcpy(config->log_file, "");

    // Override with environment variables
    char* env_val;
    
    if ((env_val = getenv("MCPSPY_MONITOR_STDIO"))) {
        config->monitor_stdio = atoi(env_val);
    }
    if ((env_val = getenv("MCPSPY_MONITOR_HTTP"))) {
        config->monitor_http = atoi(env_val);
    }
    if ((env_val = getenv("MCPSPY_MONITOR_HTTPS"))) {
        config->monitor_https = atoi(env_val);
    }
    if ((env_val = getenv("MCPSPY_MONITOR_SOCKETS"))) {
        config->monitor_sockets = atoi(env_val);
    }
    if ((env_val = getenv("MCPSPY_MONITOR_PACKETS"))) {
        config->monitor_packets = atoi(env_val);
    }
    if ((env_val = getenv("MCPSPY_LOG_FILE"))) {
        strncpy(config->log_file, env_val, sizeof(config->log_file) - 1);
    }
    if ((env_val = getenv("MCPSPY_LOG_LEVEL"))) {
        config->log_level = atoi(env_val);
    }
}

// CGO interface functions
int mcpspy_start_monitoring(const char* config_json) {
    // For now, use default config - TODO: parse JSON config
    (void)config_json; // Suppress unused parameter warning
    return mcpspy_init(NULL);
}

int mcpspy_stop_monitoring(void) {
    mcpspy_cleanup();
    return 0;
}

int mcpspy_get_next_event(mcp_event_t* event, int timeout_ms) {
    if (!event) {
        return -1;
    }

    pthread_mutex_lock(&queue_mutex);
    
    // Wait for event with timeout
    if (queue_head == queue_tail) {
        if (timeout_ms <= 0) {
            pthread_mutex_unlock(&queue_mutex);
            return 0; // No events available
        }
        
        struct timespec timeout;
        clock_gettime(CLOCK_REALTIME, &timeout);
        timeout.tv_sec += timeout_ms / 1000;
        timeout.tv_nsec += (timeout_ms % 1000) * 1000000;
        
        if (pthread_cond_timedwait(&queue_cond, &queue_mutex, &timeout) != 0) {
            pthread_mutex_unlock(&queue_mutex);
            return 0; // Timeout
        }
    }
    
    if (queue_head != queue_tail) {
        memcpy(event, &event_queue[queue_head], sizeof(mcp_event_t));
        queue_head = (queue_head + 1) % EVENT_QUEUE_SIZE;
        pthread_mutex_unlock(&queue_mutex);
        return 1; // Event available
    }
    
    pthread_mutex_unlock(&queue_mutex);
    return 0;
}

// Constructor - automatically initialize when library is loaded
__attribute__((constructor))
static void mcpspy_library_init(void) {
    // Auto-initialize if environment variable is set
    if (getenv("MCPSPY_ENABLE")) {
        mcpspy_init(NULL);
    }
}

// Destructor - cleanup when library is unloaded
__attribute__((destructor))
static void mcpspy_library_cleanup(void) {
    mcpspy_cleanup();
}