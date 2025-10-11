// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __LOG_H
#define __LOG_H

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

// Log levels - matches logrus Level enumeration
// https://github.com/sirupsen/logrus/blob/master/logrus.go
#define LOG_LEVEL_PANIC 0
#define LOG_LEVEL_FATAL 1
#define LOG_LEVEL_ERROR 2
#define LOG_LEVEL_WARN 3
#define LOG_LEVEL_INFO 4
#define LOG_LEVEL_DEBUG 5
#define LOG_LEVEL_TRACE 6

volatile __u32 log_level = LOG_LEVEL_INFO;

#define LOG_ERROR(fmt, ...) bpf_printk("ERROR: " fmt, ##__VA_ARGS__)
#define LOG_WARN(fmt, ...) bpf_printk("WARN: " fmt, ##__VA_ARGS__)
#define LOG_INFO(fmt, ...) bpf_printk("INFO: " fmt, ##__VA_ARGS__)
#define LOG_DEBUG(fmt, ...)                                                    \
    do {                                                                       \
        if (log_level >= LOG_LEVEL_DEBUG) {                                    \
            bpf_printk("DEBUG: " fmt, ##__VA_ARGS__);                          \
        }                                                                      \
    } while (0)

// LOG_TRACE: Only compiled if MCPSPY_TRACE_LOG is defined
// When compiled, only executes if log level is trace
#ifdef MCPSPY_TRACE_LOG
#define LOG_TRACE(fmt, ...)                                                    \
    do {                                                                       \
        if (log_level >= LOG_LEVEL_TRACE) {                                    \
            bpf_printk("TRACE: " fmt, ##__VA_ARGS__);                          \
        }                                                                      \
    } while (0)
#else
// When MCPSPY_TRACE_LOG is not defined, LOG_TRACE is a no-op
#define LOG_TRACE(fmt, ...) ((void)0)
#endif

#endif // __LOG_H
