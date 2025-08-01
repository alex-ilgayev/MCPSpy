// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __TLS_H
#define __TLS_H

#include "types.h"
#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

// SSL session state
struct ssl_session {
    __u8 http_version;
    __u8 is_active; // Session is active (handshake completed)
};

// Map to track SSL session states by SSL context pointer
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10240); // Track up to 10K concurrent SSL sessions
    __type(key, __u64);         // SSL context pointer
    __type(value, struct ssl_session);
} ssl_sessions SEC(".maps");

// Check if data indicates HTTP/1.1 request
static __always_inline bool is_http1_data(const char *buf, __u32 size) {
    if (size < 4) {
        return false;
    }

    // Check for common HTTP/1.1 methods
    if ((buf[0] == 'G' && buf[1] == 'E' && buf[2] == 'T' && buf[3] == ' ') ||
        (buf[0] == 'P' && buf[1] == 'O' && buf[2] == 'S' && buf[3] == 'T') ||
        (buf[0] == 'P' && buf[1] == 'U' && buf[2] == 'T' && buf[3] == ' ') ||
        (buf[0] == 'H' && buf[1] == 'E' && buf[2] == 'A' && buf[3] == 'D') ||
        (size >= 6 && buf[0] == 'D' && buf[1] == 'E' && buf[2] == 'L' &&
         buf[3] == 'E' && buf[4] == 'T' && buf[5] == 'E') ||
        (size >= 5 && buf[0] == 'P' && buf[1] == 'A' && buf[2] == 'T' &&
         buf[3] == 'C' && buf[4] == 'H') ||
        (size >= 7 && buf[0] == 'O' && buf[1] == 'P' && buf[2] == 'T' &&
         buf[3] == 'I' && buf[4] == 'O' && buf[5] == 'N' && buf[6] == 'S') ||
        (size >= 7 && buf[0] == 'C' && buf[1] == 'O' && buf[2] == 'N' &&
         buf[3] == 'N' && buf[4] == 'E' && buf[5] == 'C' && buf[6] == 'T') ||
        (size >= 5 && buf[0] == 'T' && buf[1] == 'R' && buf[2] == 'A' &&
         buf[3] == 'C' && buf[4] == 'E')) {
        return true;
    }

    // Check for HTTP/1.1 response
    if (size >= 8 && buf[0] == 'H' && buf[1] == 'T' && buf[2] == 'T' &&
        buf[3] == 'P' && buf[4] == '/' && buf[5] == '1' && buf[6] == '.') {
        return true;
    }

    return false;
}

// Check if data indicates HTTP/2 connection preface
static __always_inline bool is_http2_data(const char *buf, __u32 size) {
    if (size < 24) {
        return false;
    }

    // HTTP/2 connection preface: "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
    return (buf[0] == 'P' && buf[1] == 'R' && buf[2] == 'I' && buf[3] == ' ' &&
            buf[4] == '*' && buf[5] == ' ' && buf[6] == 'H' && buf[7] == 'T' &&
            buf[8] == 'T' && buf[9] == 'P' && buf[10] == '/' &&
            buf[11] == '2' && buf[12] == '.' && buf[13] == '0' &&
            buf[14] == '\r' && buf[15] == '\n' && buf[16] == '\r' &&
            buf[17] == '\n' && buf[18] == 'S' && buf[19] == 'M' &&
            buf[20] == '\r' && buf[21] == '\n' && buf[22] == '\r' &&
            buf[23] == '\n');
}

// Process SSL data and get identified HTTP version.
// Returns HTTP_VERSION_UNKNOWN if the data is not HTTP.
static __always_inline __u8 identify_http_version(__u64 ssl_ptr,
                                                  const char *buf, __u32 size) {
    // Can't be any HTTP version with less than 4 bytes.
    if (size < 4) {
        return HTTP_VERSION_UNKNOWN;
    }

    // Read data to check protocol
    char data_buf[24];
    if (bpf_probe_read(data_buf, sizeof(data_buf), buf) != 0) {
        return HTTP_VERSION_UNKNOWN;
    }

    if (is_http2_data(data_buf, size)) {
        return HTTP_VERSION_2;
    } else if (is_http1_data(data_buf, size)) {
        return HTTP_VERSION_1;
    }

    return HTTP_VERSION_UNKNOWN;
}

#endif // __TLS_H