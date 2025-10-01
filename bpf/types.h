// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __TYPES_H
#define __TYPES_H

#include "vmlinux.h"
#include <bpf/bpf_tracing.h>

#define MAX_BUF_SIZE 64 * 1024
#define MAX_AGGREGATED_SIZE MAX_BUF_SIZE
#define TASK_COMM_LEN 16

// limit.h indicates 4096 is the max path,
// but we want to save ringbuffer space.
#define PATH_MAX 512
#define FILENAME_MAX 255

// File mode constants
#define S_IFMT 00170000 // File type mask
#define S_IFDIR 0040000 // Directory

// Taken from mm.h
#define VM_EXEC 0x00000004

// Event types
#define EVENT_READ 1
#define EVENT_WRITE 2
#define EVENT_LIBRARY 3
#define EVENT_TLS_PAYLOAD_SEND 4
#define EVENT_TLS_PAYLOAD_RECV 5
#define EVENT_TLS_FREE 6

// HTTP version constants
#define HTTP_VERSION_UNKNOWN 0
#define HTTP_VERSION_1 1
#define HTTP_VERSION_2 2

// HTTP message types
#define HTTP_MESSAGE_REQUEST 1
#define HTTP_MESSAGE_RESPONSE 2
#define HTTP_MESSAGE_UNKNOWN 3

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 4 * 1024 * 1024); // 4MB buffer
} events SEC(".maps");

// Common header for all events
// Parsed first to get the event type.
struct event_header {
    __u8 event_type;
    __u32 pid;
    __u8 comm[TASK_COMM_LEN];
};

struct data_event {
    struct event_header header;

    __u32 size;     // Actual data size
    __u32 buf_size; // Size of data in buf (may be truncated)
    __u8 buf[MAX_BUF_SIZE];
};

struct library_event {
    struct event_header header;

    __u64 inode;     // Inode number of the library file
    __u32 mnt_ns_id; // Mount namespace ID
    __u8 path[PATH_MAX];
};

struct tls_payload_event {
    struct event_header header;

    __u64 ssl_ctx;     // SSL context pointer (session identifier)
    __u32 size;        // Actual data size
    __u32 buf_size;    // Size of data in buf (may be truncated)
    __u8 http_version; // Identified HTTP version of the session
    __u8 buf[MAX_BUF_SIZE];
};

struct tls_free_event {
    struct event_header header;

    __u64 ssl_ctx; // SSL context pointer (session identifier)
};

// Stream identification for JSON aggregation
struct stream_key {
    __u32 pid;
    __u64 file_ptr; // File pointer for uniqueness
};

// JSON aggregation state (combines metadata + buffer)
struct json_aggregation_state {
    // Metadata
    __u32 accumulated_size; // Current bytes in buffer
    __u32 open_brackets;    // Running count of '{'
    __u32 close_brackets;   // Running count of '}'
    bool found_opening;     // Found initial '{'
    __u8 operation;         // EVENT_READ or EVENT_WRITE
    __u64 last_update_ns;   // Timestamp for cleanup

    // Buffer data
    __u8 data[MAX_AGGREGATED_SIZE];
};

// Map for tracking JSON streams across multiple vfs operations
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 256); // 256 * 64KB = 16MB max
    __type(key, struct stream_key);
    __type(value, struct json_aggregation_state);
} json_streams SEC(".maps");

// Temporary scratch space for creating new aggregation states
// (avoids stack overflow when initializing 64KB structures)
// Using regular array indexed by CPU ID (eBPF programs can't be preempted, so
// no race)
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 128); // Support up to 128 CPUs
    __type(key, __u32);       // CPU ID
    __type(value, struct json_aggregation_state);
} json_scratch SEC(".maps");

#endif // __TYPES_H
