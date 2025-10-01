// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __JSON_H
#define __JSON_H

#include "types.h"
#include <bpf/bpf_core_read.h>

// Context structure for bpf_loop callback
struct bracket_count_ctx {
    const char *buf;
    __u32 size;
    __u32 open_count;
    __u32 close_count;
    bool invalid;
};

// Callback function for bpf_loop to count brackets in chunks
// Processes 64-byte chunks of the buffer
static int count_brackets_callback(__u32 index, void *ctx) {
    struct bracket_count_ctx *bctx = (struct bracket_count_ctx *)ctx;

    if (bctx->invalid) {
        return 1; // Stop iteration if already invalid
    }

#define CHUNK_SIZE 64
    __u32 offset = index * CHUNK_SIZE;

    if (offset >= bctx->size) {
        return 1; // Stop iteration
    }

    __u32 remaining = bctx->size - offset;
    __u32 read_size = remaining < CHUNK_SIZE ? remaining : CHUNK_SIZE;

    char chunk[CHUNK_SIZE];
    if (bpf_probe_read(chunk, read_size, bctx->buf + offset) != 0) {
        bctx->invalid = true;
        return 1;
    }

    // Count brackets in this chunk
#pragma unroll
    for (__u32 i = 0; i < CHUNK_SIZE; i++) {
        if (i >= read_size) {
            break;
        }

        char c = chunk[i];
        if (c == '{') {
            bctx->open_count++;
        } else if (c == '}') {
            bctx->close_count++;
            // Invalid if more closing than opening brackets
            if (bctx->close_count > bctx->open_count) {
                bctx->invalid = true;
                return 1;
            }
        }
    }

    return 0; // Continue iteration
}

// Update bracket counts from a buffer segment
static __always_inline void
update_bracket_counts(struct json_aggregation_state *state, const char *buf,
                      __u32 size) {
    // Use bpf_loop to count brackets through the buffer
    struct bracket_count_ctx ctx = {
        .buf = buf,
        .size = size,
        .open_count = 0,
        .close_count = 0,
        .invalid = false,
    };

    // Max iterations: 64KB / 64 bytes = 1024
    bpf_loop(1024, count_brackets_callback, &ctx, 0);

    if (!ctx.invalid) {
        state->open_brackets += ctx.open_count;
        state->close_brackets += ctx.close_count;
    }
}

// Check if JSON is complete (matching bracket counts)
static __always_inline bool
is_json_complete(struct json_aggregation_state *state) {
    return (state->open_brackets > 0 &&
            state->open_brackets == state->close_brackets);
}

// Append buffer to aggregation state
// Returns 0 on success, -1 on overflow
static __always_inline int
append_to_aggregation(struct json_aggregation_state *state, const char *buf,
                      __u32 size) {
    // Bounds check for verifier
    if (size == 0 || size > MAX_AGGREGATED_SIZE) {
        return -1;
    }

    // Bounds check offset for verifier - clamp to valid range
    __u32 offset = state->accumulated_size;
    if (offset >= MAX_AGGREGATED_SIZE) {
        return -1;
    }
    // Explicit clamp for verifier
    offset &= (MAX_AGGREGATED_SIZE - 1);

    // Calculate remaining space
    __u32 remaining = MAX_AGGREGATED_SIZE - offset;

    // Clamp copy_size to remaining space
    __u32 copy_size = size;
    if (copy_size > remaining) {
        copy_size = remaining;
    }

    // Additional safety: ensure copy_size fits in remaining buffer
    // This helps verifier prove offset + copy_size won't overflow
    if (copy_size > (MAX_AGGREGATED_SIZE - offset)) {
        return -1;
    }

    // Explicit masks for verifier to prove bounds
    if (offset > MAX_AGGREGATED_SIZE - 1) {
        return -1;
    }
    if (copy_size > MAX_AGGREGATED_SIZE - 1) {
        return -1;
    }
    // offset &= (MAX_AGGREGATED_SIZE - 1);
    // copy_size &= (MAX_AGGREGATED_SIZE - 1);

    // Final check that verifier can track
    if (offset + copy_size > MAX_AGGREGATED_SIZE) {
        return -1;
    }

    // Now verifier can prove: state->data[offset .. offset+copy_size] is in
    // bounds
    if (bpf_probe_read(state->data + offset, copy_size, buf) != 0) {
        return -1;
    }

    state->accumulated_size = offset + copy_size;
    return 0;
}

// Submit complete JSON event to ringbuf
static __always_inline int
submit_json_event(struct stream_key *key,
                  struct json_aggregation_state *state) {
    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for aggregated event");
        return -1;
    }

    event->header.event_type = state->operation;
    event->header.pid = key->pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->size = state->accumulated_size;
    event->buf_size = state->accumulated_size < MAX_BUF_SIZE
                          ? state->accumulated_size
                          : MAX_BUF_SIZE;

    // Copy aggregated data using bpf_probe_read
    if (bpf_probe_read(event->buf, event->buf_size, state->data) != 0) {
        bpf_printk("error: failed to copy aggregated data");
        bpf_ringbuf_discard(event, 0);
        return -1;
    }

    bpf_ringbuf_submit(event, 0);
    return 0;
}

// Validate JSON by checking if buffer starts with '{' and has matching
// brackets. Counts opening '{' and closing '}' brackets to ensure valid JSON
// structure. Uses bpf_loop for efficient iteration (requires kernel 5.17+).
static __always_inline bool is_json_data(const char *buf, __u32 size) {
    if (size < 8) {
        return false;
    }

    // First, check if it starts with '{' (after whitespace)
    char check[8];
    if (bpf_probe_read(check, sizeof(check), buf) != 0) {
        return false;
    }

    bool found_opening = false;
#pragma unroll
    for (int i = 0; i < 8; i++) {
        char c = check[i];
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
            continue;
        }
        if (c == '{') {
            found_opening = true;
        }
        break;
    }

    if (!found_opening) {
        return false;
    }

    // Use bpf_loop to count brackets through the buffer
    // Scan up to MAX_BUF_SIZE (16KB)
    struct bracket_count_ctx ctx = {
        .buf = buf,
        .size = size,
        .open_count = 0,
        .close_count = 0,
        .invalid = false,
    };

    // Max iterations: 16KB / 64 bytes = 256
    bpf_loop(256, count_brackets_callback, &ctx, 0);

    if (ctx.invalid) {
        return false;
    }

    // Valid JSON must have matching bracket counts
    return (ctx.open_count > 0 && ctx.open_count == ctx.close_count);
}

#endif // __HELPERS_H