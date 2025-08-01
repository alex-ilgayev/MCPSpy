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
#define FILENAME_MAX 255

// File mode constants
#define S_IFMT  00170000  // File type mask
#define S_IFDIR 0040000   // Directory

// Event types
#define EVENT_READ 1
#define EVENT_WRITE 2
#define EVENT_LIBRARY 3
#define EVENT_TLS_SEND 4
#define EVENT_TLS_RECV 5

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

    __u64 inode;        // Inode number of the library file
    __u8 path[PATH_MAX];
};

struct tls_event {
    struct event_header header;

    __u32 size;     // Actual data size
    __u32 buf_size; // Size of data in buf (may be truncated)
    __u8 buf[MAX_BUF_SIZE];
};


// Structure to pass SSL_read parameters from uprobe to uretprobe
struct ssl_read_params {
    __u64 ssl;
    __u64 buf;
};

// Structure to pass SSL_read_ex parameters from uprobe to uretprobe
struct ssl_read_ex_params {
    __u64 ssl;
    __u64 buf;
    __u64 readbytes; // pointer to size_t
};

// Map to store SSL_read parameters
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // PID
    __type(value, struct ssl_read_params);
} ssl_read_args SEC(".maps");

// Map to store SSL_read_ex parameters
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32); // PID
    __type(value, struct ssl_read_ex_params);
} ssl_read_ex_args SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 4 * 1024 * 1024); // 4MB buffer
} events SEC(".maps");

// Checking if the buffer starts with '{', while ignoring whitespace.
static __always_inline bool is_mcp_data(const char *buf, __u32 size) {
    if (size < 8) {
        return false;
    }

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


    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for read event");
        return 0;
    }

    event->header.event_type = EVENT_READ;
    event->header.pid = bpf_get_current_pid_tgid() >> 32;
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

    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for write event");
        return 0;
    }

    event->header.event_type = EVENT_WRITE;
    event->header.pid = bpf_get_current_pid_tgid() >> 32;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->size = ret;
    event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    bpf_probe_read(event->buf, event->buf_size, buf);

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// Taken from mm.h
#define VM_EXEC 0x00000004

// Check if filename matches our criteria. Currently the options are:
// - "node"
// - "libssl"
static __always_inline bool is_filename_relevant(const char *filename) {
    // Check if filename is "node"
    if (filename[0] == 'n' && filename[1] == 'o' && filename[2] == 'd' && filename[3] == 'e' && filename[4] == '\0') {
        return true;
    }

    // Check if filename starts with "libssl"
    if (filename[0] == 'l' && filename[1] == 'i' && filename[2] == 'b' && filename[3] == 's' && filename[4] == 's' && filename[5] == 'l') {
        return true;
    }
    
    return false;
}

// Filtering out non-interesting paths in linux,
// such as /proc, /sys, /dev, /mnt, /memfd.
static __always_inline bool is_path_relevant(const char *path) {
    if (path[0] == '/' && path[1] == 'p' && path[2] == 'r' && path[3] == 'o' && path[4] == 'c' && path[5] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 's' && path[2] == 'y' && path[3] == 's' && path[4] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'd' && path[2] == 'e' && path[3] == 'v' && path[4] == '/') {
        return false;
    }
    
    if (path[0] == '/' && path[1] == 'm' && path[2] == 'n' && path[3] == 't' && path[4] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'm' && path[2] == 'e' && path[3] == 'm' && path[4] == 'f') {
        return false;
    }

    return true;
}

// Check if dentry is a directory.
static __always_inline bool is_directory(struct dentry *dentry) {
    if (!dentry) {
        return false;
    }
    
    struct inode *inode = dentry->d_inode;
    if (!inode) {
        return false;
    }
    
    return (inode->i_mode & S_IFMT) == S_IFDIR;
}

// Enumerate loaded modules across all processes.
// To improve the performance, we filter out non-interesting filenames,
// and non-interesting root directories.
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

    // Check if is an interesting library name
    char filename[FILENAME_MAX];
    __builtin_memset(filename, 0, FILENAME_MAX);
    bpf_probe_read_kernel(filename, FILENAME_MAX, file->f_path.dentry->d_name.name);
    if (!is_filename_relevant(filename)) {
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
    event->inode = file->f_inode->i_ino;
    bpf_probe_read_kernel_str(&event->header.comm, sizeof(event->header.comm), task->comm);
    __builtin_memset(event->path, 0, PATH_MAX);
    bpf_d_path(&file->f_path, (char *)event->path, PATH_MAX);

    if (!is_path_relevant((const char *)event->path)) {
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// Track when files are opened to detect dynamic library loading
// We use security_file_open and not security_file_mprotect
// because we want to get the full path through bpf_d_path,
// and there is limited probes that allow us to do that.
// We do not want to use LSM hooks for now.
//
// To improve the performance, we filter out non-interesting filenames,
// and non-interesting root directories.
SEC("fentry/security_file_open")
int BPF_PROG(trace_security_file_open, struct file *file) {
    if (!file) {
        return 0;
    }
    
    // Check if directory
    if (is_directory(file->f_path.dentry)) {
        return 0;
    }

    char filename[FILENAME_MAX];
    __builtin_memset(filename, 0, FILENAME_MAX);
    bpf_probe_read_kernel(filename, FILENAME_MAX, file->f_path.dentry->d_name.name);

    // Checking if filename matches to what we looking for.
    if (!is_filename_relevant(filename)) {
        return 0;
    }

    struct library_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct library_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for security file open event");
        return 0;
    }
    
    __builtin_memset(event->path, 0, PATH_MAX);
    bpf_d_path(&file->f_path, (char *)event->path, PATH_MAX);
    
    event->header.event_type = EVENT_LIBRARY;
    event->header.pid = bpf_get_current_pid_tgid() >> 32;
    event->inode = file->f_inode->i_ino;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));

    if (!is_path_relevant((const char *)event->path)) {
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);
    
    return 0;
}

SEC("uprobe/SSL_read")
int BPF_UPROBE(ssl_read_entry, void *ssl, void *buf) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    struct ssl_read_params params = {
        .ssl = (__u64)ssl,
        .buf = (__u64)buf,
    };
    
    bpf_map_update_elem(&ssl_read_args, &pid, &params, BPF_ANY);
    return 0;
}

SEC("uretprobe/SSL_read")
int BPF_URETPROBE(ssl_read_exit, int ret) {
    if (ret <= 0) {
        return 0;
    }
    
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    // Retrieve the entry parameters
    struct ssl_read_params *params = bpf_map_lookup_elem(&ssl_read_args, &pid);
    if (!params) {
        return 0;
    }
    bpf_map_delete_elem(&ssl_read_args, &pid);

    struct tls_event *event = bpf_ringbuf_reserve(&events, sizeof(struct tls_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_read event");
        return 0;
    }
    
    event->header.event_type = EVENT_TLS_RECV;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));

    // Ensure buf_size is within bounds and positive for the verifier
    __u32 size = (__u32)ret;
    size &= 0x7FFFFFFF; // Ensure it's positive by clearing the sign bit
    event->size = size;
    event->buf_size = size > MAX_BUF_SIZE ? MAX_BUF_SIZE : size;

    if (bpf_probe_read(&event->buf, event->buf_size, (const void *)params->buf) != 0) {
        bpf_printk("error: failed to read SSL_read data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }
    
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uprobe/SSL_write")
int BPF_UPROBE(ssl_write_entry, void *ssl, const void *buf, int num) {
    if (num <= 0) {
        return 0;
    }
    
    struct tls_event *event = bpf_ringbuf_reserve(&events, sizeof(struct tls_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_write event");
        return 0;
    }
    
    event->header.event_type = EVENT_TLS_SEND;
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));    

    // Ensure buf_size is within bounds and positive for the verifier
    __u32 size = (__u32)num;
    size &= 0x7FFFFFFF; // Ensure it's positive by clearing the sign bit
    event->size = size;
    event->buf_size = size > MAX_BUF_SIZE ? MAX_BUF_SIZE : size;
    
    if (bpf_probe_read(&event->buf, event->buf_size, buf) != 0) {
        bpf_printk("error: failed to read SSL_write data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }
    
    bpf_ringbuf_submit(event, 0);

    return 0;
}

SEC("uprobe/SSL_read_ex")
int BPF_UPROBE(ssl_read_ex_entry, void *ssl, void *buf, size_t num, size_t *readbytes) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    struct ssl_read_ex_params params = {
        .ssl = (__u64)ssl,
        .buf = (__u64)buf,
        .readbytes = (__u64)readbytes
    };
    
    bpf_map_update_elem(&ssl_read_ex_args, &pid, &params, BPF_ANY);
    return 0;
}

SEC("uretprobe/SSL_read_ex")
int BPF_URETPROBE(ssl_read_ex_exit, int ret) {
    // We only care about successful reads.
    if (ret != 1) { 
        return 0;
    }
    
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    
    // Retrieve the entry parameters
    struct ssl_read_ex_params *params = bpf_map_lookup_elem(&ssl_read_ex_args, &pid);
    if (!params) {
        return 0;
    }
    bpf_map_delete_elem(&ssl_read_ex_args, &pid);
    
    // Try to read the actual bytes read from the readbytes pointer
    size_t actual_read = 0;
    if (params->readbytes) {
        bpf_probe_read(&actual_read, sizeof(actual_read), (const void *)params->readbytes);
    }
    
    struct tls_event *event = bpf_ringbuf_reserve(&events, sizeof(struct tls_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_read_ex event");
        return 0;
    }
    
    event->header.event_type = EVENT_TLS_RECV;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->size = actual_read;
    event->buf_size = actual_read > MAX_BUF_SIZE ? MAX_BUF_SIZE : actual_read;

    if (bpf_probe_read(&event->buf, event->buf_size, (const void *)params->buf) != 0) {
        bpf_printk("error: failed to read SSL_read_ex data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }
    
    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uprobe/SSL_write_ex")
int BPF_UPROBE(ssl_write_ex_entry, void *ssl, const void *buf, size_t num, size_t *written) {
    if (num <= 0) {
        return 0;
    }
    
    struct tls_event *event = bpf_ringbuf_reserve(&events, sizeof(struct tls_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_write_ex event");
        return 0;
    }
    
    event->header.event_type = EVENT_TLS_SEND;
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));    
    event->size = num;
    event->buf_size = num > MAX_BUF_SIZE ? MAX_BUF_SIZE : num;

    if (bpf_probe_read(&event->buf, event->buf_size, buf) != 0) {
        bpf_printk("error: failed to read SSL_write_ex data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }
    
    bpf_ringbuf_submit(event, 0);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";