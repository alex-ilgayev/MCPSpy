#include "libmcpspy.h"
#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/x509.h>
#include <openssl/pem.h>
#include <openssl/rsa.h>

// SSL monitoring state
static int ssl_initialized = 0;
static pthread_t ssl_thread;
static int ssl_running = 0;

// Common HTTPS ports to monitor
static int https_ports[] = {443, 8443, 9443, 0};

// Original SSL function pointers (if OpenSSL is used)
static int (*original_SSL_read)(SSL *ssl, void *buf, int num) = NULL;
static int (*original_SSL_write)(SSL *ssl, const void *buf, int num) = NULL;

// Check if port is HTTPS
int is_https_port(int port) {
    for (int i = 0; https_ports[i] != 0; i++) {
        if (port == https_ports[i]) {
            return 1;
        }
    }
    return 0;
}

// Load original SSL functions if OpenSSL is available
static void load_ssl_functions(void) {
    if (original_SSL_read == NULL) {
        original_SSL_read = (int (*)(SSL *, void *, int))dlsym(RTLD_NEXT, "SSL_read");
    }
    if (original_SSL_write == NULL) {
        original_SSL_write = (int (*)(SSL *, const void *, int))dlsym(RTLD_NEXT, "SSL_write");
    }
}

// Hooked SSL_read function
int SSL_read(SSL *ssl, void *buf, int num) {
    if (!original_SSL_read) {
        load_ssl_functions();
    }
    
    if (!original_SSL_read) {
        errno = ENOSYS;
        return -1;
    }

    int result = original_SSL_read(ssl, buf, num);
    
    if (result > 0 && g_initialized && g_config.monitor_https) {
        // Get the underlying file descriptor
        int fd = SSL_get_fd(ssl);
        if (fd >= 0) {
            create_and_log_event(fd, buf, result, EVENT_TYPE_READ, TRANSPORT_HTTPS);
        }
    }

    return result;
}

// Hooked SSL_write function
int SSL_write(SSL *ssl, const void *buf, int num) {
    if (!original_SSL_write) {
        load_ssl_functions();
    }
    
    if (!original_SSL_write) {
        errno = ENOSYS;
        return -1;
    }

    int result = original_SSL_write(ssl, buf, num);
    
    if (result > 0 && g_initialized && g_config.monitor_https) {
        // Get the underlying file descriptor
        int fd = SSL_get_fd(ssl);
        if (fd >= 0) {
            create_and_log_event(fd, buf, result, EVENT_TYPE_WRITE, TRANSPORT_HTTPS);
        }
    }

    return result;
}

// Generate self-signed certificate for MITM (if enabled)
static int generate_mitm_certificate(void) {
    if (!g_config.enable_ssl_mitm) {
        return 0; // MITM not enabled
    }

    // Check if certificate files already exist
    if (access(g_config.cert_file, F_OK) == 0 && access(g_config.key_file, F_OK) == 0) {
        return 0; // Certificate files already exist
    }

    // This is a simplified version - in production, you'd want proper certificate generation
    // For now, we'll just log that MITM is requested but not implemented
    fprintf(stderr, "mcpspy: SSL MITM requested but certificate generation not implemented\n");
    fprintf(stderr, "mcpspy: Please provide certificate files: %s and %s\n", 
            g_config.cert_file, g_config.key_file);
    
    return -1;
}

// SSL monitoring thread
static void* ssl_monitor_thread(void* arg) {
    (void)arg; // Suppress unused parameter warning
    // This thread could implement SSL proxy or certificate management
    // For now, SSL monitoring is handled by the SSL_read/SSL_write hooks
    
    while (ssl_running) {
        usleep(100000); // 100ms
    }
    
    return NULL;
}

// Initialize SSL transport monitoring
int ssl_monitor_init(void) {
    if (ssl_initialized) {
        return 0; // Already initialized
    }

    // Initialize OpenSSL if not already done
    SSL_library_init();
    SSL_load_error_strings();
    OpenSSL_add_all_algorithms();

    // Load SSL function pointers
    load_ssl_functions();

    // Generate or check MITM certificate if enabled
    if (generate_mitm_certificate() != 0 && g_config.enable_ssl_mitm) {
        fprintf(stderr, "mcpspy: Failed to setup SSL MITM certificate\n");
        return -1;
    }
    
    ssl_running = 1;
    if (pthread_create(&ssl_thread, NULL, ssl_monitor_thread, NULL) != 0) {
        fprintf(stderr, "mcpspy: Failed to create SSL monitoring thread\n");
        return -1;
    }
    
    ssl_initialized = 1;
    return 0;
}

// Cleanup SSL monitoring
void ssl_monitor_cleanup(void) {
    if (!ssl_initialized) {
        return;
    }
    
    ssl_running = 0;
    pthread_join(ssl_thread, NULL);
    
    // Cleanup OpenSSL
    EVP_cleanup();
    ERR_free_strings();
    
    ssl_initialized = 0;
}