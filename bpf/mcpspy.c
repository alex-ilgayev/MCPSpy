// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define MAX_BUF_SIZE 16 * 1024
#define TASK_COMM_LEN 16

// limit.h indicates 4096 is the max path,
// but we want to save ringbuffer space.
#define PATH_MAX 512

// Event types
#define EVENT_READ 1
#define EVENT_WRITE 2
#define EVENT_LIBRARY 3

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

    __u8 path[PATH_MAX];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 4 * 1024 * 1024); // 4MB buffer
} events SEC(".maps");

// Checking if the buffer starts with '{', while ignoring whitespace.
static __always_inline bool is_mcp_data(const char *buf, __u32 size) {
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

    if (!is_mcp_data(buf, ret)) {
        return 0;
    }

    pid_t tgid = bpf_get_current_pid_tgid();

    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for read event");
        return 0;
    }

    event->header.event_type = EVENT_READ;
    event->header.pid = tgid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->size = ret;
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

    if (!is_mcp_data(buf, ret)) {
        return 0;
    }

    pid_t tgid = bpf_get_current_pid_tgid();

    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for write event");
        return 0;
    }

    event->header.event_type = EVENT_WRITE;
    event->header.pid = tgid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->size = ret;
    event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    bpf_probe_read(event->buf, event->buf_size, buf);

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// Taken from mm.h
#define VM_EXEC 0x00000004

// Doing simple check if indicates a file in fs.
// - Checking if starts with '/'
// - Checking if is not starting with "/memfd:"
static __always_inline bool is_file_path(const char *path) {
    if (path[0] == '\0') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'm' && path[2] == 'e' && path[3] == 'm' &&
        path[4] == 'f' && path[5] == 'd' && path[6] == ':') {
        return false;
    }

    if (path[0] == '/') {
        return true;
    }

    return false;
}

SEC("iter/task_vma")
int enumerate_loaded_modules(struct bpf_iter__task_vma *ctx) {
    struct task_struct *task = ctx->task;
    struct vm_area_struct *vma = ctx->vma;

    // If no task or vma, we're done
    if (!task || !vma) {
        return 0;
    }

    // Check if this VMA is a file mapping
    struct file *file = vma->vm_file;
    if (!file) {
        return 0;
    }

    // Check if is executable (indication of library)
    if (!(vma->vm_flags & VM_EXEC)) {
        return 0;
    }

    // Send library event to userspace
    struct library_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct library_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for library event");
        return 0;
    }

    event->header.event_type = EVENT_LIBRARY;
    event->header.pid = task->tgid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    __builtin_memset(event->path, 0, PATH_MAX);
    bpf_d_path(&file->f_path, (char *)event->path, PATH_MAX);

    if (!is_file_path((char *)event->path)) {
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";