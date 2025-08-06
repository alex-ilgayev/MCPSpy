// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __TYPES_H
#define __TYPES_H

#include "vmlinux.h"
#include <bpf/bpf_tracing.h>

#define MAX_BUF_SIZE 16 * 1024
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
#define EVENT_TLS_SEND 4
#define EVENT_TLS_RECV 5

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

    __u64 inode; // Inode number of the library file
    __u8 path[PATH_MAX];
};

struct tls_event {
    struct event_header header;

    __u64 ssl_ctx;     // SSL context pointer (session identifier)
    __u32 size;        // Actual data size
    __u32 buf_size;    // Size of data in buf (may be truncated)
    __u8 http_version; // Identified HTTP version of the session
    __u8 buf[MAX_BUF_SIZE];
};

#endif // __TYPES_H
