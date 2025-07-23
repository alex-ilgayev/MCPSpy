// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define MAX_BUF_SIZE 16 * 1024
#define TASK_COMM_LEN 16

// Event types
#define EVENT_READ 1
#define EVENT_WRITE 2
#define EVENT_SSL_READ 3
#define EVENT_SSL_WRITE 4

// Event structure sent to userspace
struct event {
    __u32 pid;
    __u8 comm[TASK_COMM_LEN];
    __u8 event_type;
    __u32 size;
    __u32 buf_size;
    __u8 buf[MAX_BUF_SIZE];
};

// Ring buffer for sending events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 4 * 1024 * 1024); // 4MB buffer
} events SEC(".maps");

// Checking if the buffer starts with '{', while ignoring whitespace.
static __always_inline bool is_json_data(const char *buf, __u32 size) {
    if (size < 1)
        return false;

    char check[8];
    if (bpf_probe_read(check, sizeof(check), buf) != 0) {
        return false;
    }

// Check the first 8 bytes for the first non-whitespace character being '{'
#pragma unroll
    for (int i = 0; i < 8; i++) {
        char c = check[i];
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
            continue;
        }
        if (c == '{') {
            return true;
        }
        break;
    }
    return false;
}

SEC("fexit/vfs_read")
int BPF_PROG(exit_vfs_read, struct file *file, const char *buf, size_t count,
             loff_t *_pos, ssize_t ret) {
    if (ret <= 0) {
        // We logging only operations with data.
        return 0;
    }

    if (!is_json_data(buf, ret)) {
        return 0;
    }

    pid_t tgid = bpf_get_current_pid_tgid();

    struct event *event = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for read event");
        return 0;
    }

    event->pid = tgid;
    event->event_type = EVENT_READ;
    event->size = ret;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    bpf_probe_read(event->buf, event->buf_size, buf);

    bpf_ringbuf_submit(event, 0);

    return 0;
}

SEC("fexit/vfs_write")
int BPF_PROG(exit_vfs_write, struct file *file, const char *buf, size_t count,
             loff_t *_pos, size_t ret) {
    if (ret <= 0) {
        // We logging only operations with data.
        return 0;
    }

    if (!is_json_data(buf, ret)) {
        return 0;
    }

    pid_t tgid = bpf_get_current_pid_tgid();

    struct event *event = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for write event");
        return 0;
    }

    event->pid = tgid;
    event->event_type = EVENT_WRITE;
    event->size = ret;
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    bpf_probe_read(event->buf, event->buf_size, buf);

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// SSL_read hook
// int SSL_read(SSL *ssl, void *buf, int num);
// SEC("uprobe/SSL_read")
// int uprobe_ssl_read(struct pt_regs *ctx) {
//     // SSL_read returns the number of bytes read
//     int ret = PT_REGS_RC(ctx);
//     if (ret <= 0) {
//         return 0;
//     }

//     // Get the buffer pointer (second argument)
//     void *buf = (void *)PT_REGS_PARM2(ctx);

//     pid_t tgid = bpf_get_current_pid_tgid() >> 32;

//     struct event *event = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
//     if (!event) {
//         bpf_printk("error: failed to reserve ring buffer for SSL read event");
//         return 0;
//     }

//     event->pid = tgid;
//     event->event_type = EVENT_SSL_READ;
//     event->size = ret;
//     bpf_get_current_comm(&event->comm, sizeof(event->comm));
//     event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
//     bpf_probe_read(event->buf, event->buf_size, buf);

//     bpf_ringbuf_submit(event, 0);

//     return 0;
// }

// SSL_write hook
// int SSL_write(SSL *ssl, const void *buf, int num);
SEC("uretprobe/SSL_write")
int uretprobe_ssl_write(struct pt_regs *ctx) {
    // SSL_write returns the number of bytes written
    int ret = PT_REGS_RC(ctx);
    if (ret <= 0) {
        return 0;
    }

    bpf_printk("SSL_write ret: %d", ret);

    // // Get the buffer pointer (second argument)
    // const void *buf = (const void *)PT_REGS_PARM2(ctx);

    // pid_t tgid = bpf_get_current_pid_tgid() >> 32;

    // struct event *event = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    // if (!event) {
    //     bpf_printk("error: failed to reserve ring buffer for SSL write event");
    //     return 0;
    // }

    // event->pid = tgid;
    // event->event_type = EVENT_SSL_WRITE;
    // event->size = ret;
    // bpf_get_current_comm(&event->comm, sizeof(event->comm));
    // event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    // bpf_probe_read(event->buf, event->buf_size, buf);

    // bpf_ringbuf_submit(event, 0);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";