// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __ARGS_H
#define __ARGS_H

#include "vmlinux.h"
#include <bpf/bpf_tracing.h>

struct ssl_read_params {
    __u64 ssl;
    __u64 buf;
};

struct ssl_read_ex_params {
    __u64 ssl;
    __u64 buf;
    __u64 readbytes; // pointer to size_t
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // PID
    __type(value, struct ssl_read_params);
} ssl_read_args SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // PID
    __type(value, struct ssl_read_ex_params);
} ssl_read_ex_args SEC(".maps");

#endif // __ARGS_H