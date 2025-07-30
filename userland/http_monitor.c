#include "libmcpspy.h"

// HTTP monitoring state
static int http_initialized = 0;
static pthread_t http_thread;
static int http_running = 0;

// Common HTTP ports to monitor
static int http_ports[] = {80, 8080, 3000, 4000, 5000, 8000, 9000, 0};

// Check if port is HTTP
int is_http_port(int port) {
    for (int i = 0; http_ports[i] != 0; i++) {
        if (port == http_ports[i]) {
            return 1;
        }
    }
    return 0;
}

// HTTP monitoring thread
static void* http_monitor_thread(void* arg) {
    (void)arg; // Suppress unused parameter warning
    // This thread could implement additional HTTP-specific monitoring
    // For now, HTTP monitoring is handled by the socket hooks in main library
    
    while (http_running) {
        usleep(100000); // 100ms
    }
    
    return NULL;
}

// Initialize HTTP transport monitoring
int http_monitor_init(void) {
    if (http_initialized) {
        return 0; // Already initialized
    }

    // HTTP monitoring is primarily handled by socket hooks
    // This could be extended for HTTP-specific parsing, proxy setup, etc.
    
    http_running = 1;
    if (pthread_create(&http_thread, NULL, http_monitor_thread, NULL) != 0) {
        fprintf(stderr, "mcpspy: Failed to create HTTP monitoring thread\n");
        return -1;
    }
    
    http_initialized = 1;
    return 0;
}

// Cleanup HTTP monitoring
void http_monitor_cleanup(void) {
    if (!http_initialized) {
        return;
    }
    
    http_running = 0;
    pthread_join(http_thread, NULL);
    
    http_initialized = 0;
}