// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#include "args.h"
#include "helpers.h"
#include "log.h"
#include "tls.h"
#include "types.h"
#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>

// Checking if the buffer starts with '{', while ignoring whitespace.
static __always_inline bool is_json(const char *buf, __u32 size) {
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

    // Check if inode is a pipe - we only track stdio (pipe) communication
    // This is cheaper than buffer inspection, so check it first
    if (!is_pipe(file)) {
        return 0;
    }

    if (!is_json(buf, ret)) {
        return 0;
    }

    LOG_TRACE("exit_vfs_read: file=%p, count=%d, ret=%d", file, buf, count,
              ret);

    if (ret > MAX_BUF_SIZE) {
        // Currently the strategy is to drop incomplete fs events.
        // These events would fail in the JSON parsing anyways.
        LOG_DEBUG("exit_vfs_read: dropping read event with count %d > %d", ret,
                  MAX_BUF_SIZE);
        return 0;
    }

    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    // Lookup or create cache entry
    // Note: Inode numbers are only unique within a filesystem. If monitoring
    // processes across multiple filesystems or mount namespaces, inode
    // collisions are possible but rare in practice for pipe-based stdio.
    __u32 inode = BPF_CORE_READ(file, f_inode, i_ino);
    struct inode_process_info *info =
        bpf_map_lookup_elem(&inode_process_map, &inode);
    if (!info) {
        struct inode_process_info new_info = {0};
        new_info.reader_pid = bpf_get_current_pid_tgid() >> 32;
        bpf_get_current_comm(new_info.reader_comm,
                             sizeof(new_info.reader_comm));
        if (bpf_map_update_elem(&inode_process_map, &inode, &new_info,
                                BPF_ANY)) {
            LOG_WARN("exit_vfs_read: failed to create inode_process map entry");
            return 0;
        }

        // We can't report the event yet, as we don't have the writer info.
        return 0;
    } else {
        // Check if reader slot is already filled with another pid
        if (info->reader_pid != 0 && info->reader_pid != pid) {
            // Even though this shouldn't happen for pipes, this can happen for
            // other cases. In case of pipes this is unordinary, so we remove it
            // from the cache.
            bpf_map_delete_elem(&inode_process_map, &inode);
            return 0;
        }

        // Empty reader slot, fill it
        if (info->reader_pid == 0) {
            info->reader_pid = pid;
            bpf_get_current_comm(info->reader_comm, sizeof(info->reader_comm));
            if (bpf_map_update_elem(&inode_process_map, &inode, info,
                                    BPF_ANY)) {
                LOG_WARN(
                    "exit_vfs_read: failed to update inode_process map entry");
                return 0;
            }
        }

        // Check if we have a writer
        if (info->writer_pid == 0) {
            // We can't report the event yet, as we don't have the writer info.
            return 0;
        }
    }

    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        LOG_WARN("exit_vfs_read: failed to reserve ring buffer for read event");
        return 0;
    }

    event->header.event_type = EVENT_READ;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->inode = inode;
    event->from_pid = info->writer_pid;
    __builtin_memcpy(event->from_comm, info->writer_comm, TASK_COMM_LEN);
    event->to_pid = info->reader_pid;
    __builtin_memcpy(event->to_comm, info->reader_comm, TASK_COMM_LEN);
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

    // Check if inode is a pipe - we only track stdio (pipe) communication
    // This is cheaper than buffer inspection, so check it first
    if (!is_pipe(file)) {
        return 0;
    }

    if (!is_json(buf, ret)) {
        return 0;
    }

    LOG_TRACE("exit_vfs_write: file=%p, count=%d, ret=%d", file, count, ret);

    if (ret > MAX_BUF_SIZE) {
        // Currently the strategy is to drop incomplete fs events.
        // These events would fail in the JSON parsing anyways.
        LOG_DEBUG("exit_vfs_write: dropping write event with count %d > %d",
                  ret, MAX_BUF_SIZE);
        return 0;
    }

    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    // Lookup or create cache entry
    // Note: Inode numbers are only unique within a filesystem. If monitoring
    // processes across multiple filesystems or mount namespaces, inode
    // collisions are possible but rare in practice for pipe-based stdio.
    __u32 inode = BPF_CORE_READ(file, f_inode, i_ino);
    struct inode_process_info *info =
        bpf_map_lookup_elem(&inode_process_map, &inode);
    if (!info) {
        struct inode_process_info new_info = {0};
        new_info.writer_pid = bpf_get_current_pid_tgid() >> 32;
        bpf_get_current_comm(new_info.writer_comm,
                             sizeof(new_info.writer_comm));
        if (bpf_map_update_elem(&inode_process_map, &inode, &new_info,
                                BPF_ANY)) {
            LOG_WARN(
                "exit_vfs_write: failed to create inode_process map entry");
            return 0;
        }

        // We can't report the event yet, as we don't have the reader info.
        return 0;
    } else {
        // Check if writer slot is already filled with another pid
        if (info->writer_pid != 0 && info->writer_pid != pid) {
            // Even though this shouldn't happen for pipes, this can happen for
            // other cases. In case of pipes this is unordinary, so we remove it
            // from the cache.
            bpf_map_delete_elem(&inode_process_map, &inode);
            return 0;
        }

        // Empty writer slot, fill it
        if (info->writer_pid == 0) {
            info->writer_pid = pid;
            bpf_get_current_comm(info->writer_comm, sizeof(info->writer_comm));
            if (bpf_map_update_elem(&inode_process_map, &inode, info,
                                    BPF_ANY)) {
                LOG_WARN(
                    "exit_vfs_write: failed to update inode_process map entry");
                return 0;
            }
        }

        // Check if we have a reader
        if (info->reader_pid == 0) {
            // We can't report the event yet, as we don't have the reader info.
            return 0;
        }
    }

    struct data_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct data_event), 0);
    if (!event) {
        LOG_WARN(
            "exit_vfs_write: failed to reserve ring buffer for write event");
        return 0;
    }

    event->header.event_type = EVENT_WRITE;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->inode = inode;
    event->from_pid = info->writer_pid;
    __builtin_memcpy(event->from_comm, info->writer_comm, TASK_COMM_LEN);
    event->to_pid = info->reader_pid;
    __builtin_memcpy(event->to_comm, info->reader_comm, TASK_COMM_LEN);
    event->size = ret;
    event->buf_size = ret < MAX_BUF_SIZE ? ret : MAX_BUF_SIZE;
    bpf_probe_read(event->buf, event->buf_size, buf);

    bpf_ringbuf_submit(event, 0);

    return 0;
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
    bpf_probe_read_kernel(filename, FILENAME_MAX,
                          file->f_path.dentry->d_name.name);
    if (!is_filename_relevant(filename)) {
        return 0;
    }

    // Send library event to userspace
    struct library_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct library_event), 0);
    if (!event) {
        LOG_WARN("enumerate_loaded_modules: failed to reserve ring buffer for "
                 "library event");
        return 0;
    }

    event->header.event_type = EVENT_LIBRARY;
    event->header.pid = task->tgid;
    event->inode = file->f_inode->i_ino;
    event->mnt_ns_id = get_mount_ns_id();
    bpf_probe_read_kernel_str(&event->header.comm, sizeof(event->header.comm),
                              task->comm);
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
    bpf_probe_read_kernel(filename, FILENAME_MAX,
                          file->f_path.dentry->d_name.name);

    // Checking if filename matches to what we looking for.
    if (!is_filename_relevant(filename)) {
        return 0;
    }

    struct library_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct library_event), 0);
    if (!event) {
        LOG_WARN("security_file_open: failed to reserve ring buffer for "
                 "security file open event");
        return 0;
    }

    __builtin_memset(event->path, 0, PATH_MAX);
    bpf_d_path(&file->f_path, (char *)event->path, PATH_MAX);

    event->header.event_type = EVENT_LIBRARY;
    event->header.pid = bpf_get_current_pid_tgid() >> 32;
    event->inode = file->f_inode->i_ino;
    event->mnt_ns_id = get_mount_ns_id();
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
    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    // Retrieve the entry parameters
    struct ssl_read_params *params = bpf_map_lookup_elem(&ssl_read_args, &pid);
    if (!params) {
        return 0;
    }
    bpf_map_delete_elem(&ssl_read_args, &pid);

    // We only care about successful reads.
    if (ret <= 0) {
        return 0;
    }

    LOG_TRACE("ssl_read_exit: ctx=%p, ret=%d", (void *)params->ssl, ret);

    if (ret > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        LOG_DEBUG("ssl_read_exit: buffer is too big: %d > %d", ret,
                  MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = params->ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
        LOG_TRACE("ssl_read_exit: session not found");
        return 0;
    }

    __u8 http_version = HTTP_VERSION_UNKNOWN;
    __u8 http_message_type = HTTP_MESSAGE_UNKNOWN;
    if (session->http_version == HTTP_VERSION_UNKNOWN) {
        identify_http_version(ssl_ptr, (const char *)params->buf, ret,
                              &http_version, &http_message_type);

        if (http_version == HTTP_VERSION_UNKNOWN) {
            return 0;
        }

        LOG_TRACE("ssl_read_exit: http_version=%d, http_message_type=%d",
                  http_version, http_message_type);

        // We only care about HTTP clients (not servers).
        // ssl_read should be called only for responses.
        if (http_message_type == HTTP_MESSAGE_REQUEST) {
            return 0;
        }

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        LOG_WARN(
            "ssl_read_exit: failed to reserve ring buffer for SSL_read event");
        return 0;
    }

    event->header.event_type = EVENT_TLS_PAYLOAD_RECV;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->ssl_ctx = ssl_ptr;
    event->http_version = session->http_version;

    // Ensure buf_size is within bounds and positive for the verifier
    __u32 size = (__u32)ret;
    size &= 0x7FFFFFFF; // Ensure it's positive by clearing the sign bit
    event->size = size;
    event->buf_size = size > MAX_BUF_SIZE ? MAX_BUF_SIZE : size;

    if (bpf_probe_read(&event->buf, event->buf_size,
                       (const void *)params->buf) != 0) {
        LOG_WARN("ssl_read_exit: failed to read SSL_read data");
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

    if (num > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        LOG_DEBUG("ssl_write_entry: buffer is too big: %d > %d", num,
                  MAX_BUF_SIZE);
    }

    LOG_TRACE("ssl_write_entry: ctx=%p, num=%d", ssl, num);

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = (__u64)ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
        LOG_TRACE("ssl_write_entry: session not found");
        return 0;
    }

    __u8 http_version = HTTP_VERSION_UNKNOWN;
    __u8 http_message_type = HTTP_MESSAGE_UNKNOWN;
    if (session->http_version == HTTP_VERSION_UNKNOWN) {
        identify_http_version(ssl_ptr, buf, num, &http_version,
                              &http_message_type);

        if (http_version == HTTP_VERSION_UNKNOWN) {
            return 0;
        }

        // We only care about HTTP clients (not servers).
        // SSL_write should be called only for requests.
        if (http_message_type == HTTP_MESSAGE_RESPONSE) {
            return 0;
        }

        LOG_TRACE("ssl_write_entry: http_version=%d, http_message_type=%d",
                  http_version, http_message_type);

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        LOG_WARN("ssl_write_entry: failed to reserve ring buffer for SSL_write "
                 "event");
        return 0;
    }

    event->header.event_type = EVENT_TLS_PAYLOAD_SEND;
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->ssl_ctx = ssl_ptr;
    event->http_version = session->http_version;

    // Ensure buf_size is within bounds and positive for the verifier
    __u32 size = (__u32)num;
    size &= 0x7FFFFFFF; // Ensure it's positive by clearing the sign bit
    event->size = size;
    event->buf_size = size > MAX_BUF_SIZE ? MAX_BUF_SIZE : size;

    if (bpf_probe_read(&event->buf, event->buf_size, buf) != 0) {
        LOG_WARN("ssl_write_entry: failed to read SSL_write data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);

    return 0;
}

SEC("uprobe/SSL_read_ex")
int BPF_UPROBE(ssl_read_ex_entry, void *ssl, void *buf, size_t num,
               size_t *readbytes) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    struct ssl_read_ex_params params = {
        .ssl = (__u64)ssl, .buf = (__u64)buf, .readbytes = (__u64)readbytes};

    bpf_map_update_elem(&ssl_read_ex_args, &pid, &params, BPF_ANY);
    return 0;
}

SEC("uretprobe/SSL_read_ex")
int BPF_URETPROBE(ssl_read_ex_exit, int ret) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    // Retrieve the entry parameters
    struct ssl_read_ex_params *params =
        bpf_map_lookup_elem(&ssl_read_ex_args, &pid);
    if (!params) {
        return 0;
    }
    bpf_map_delete_elem(&ssl_read_ex_args, &pid);

    // We only care about successful reads.
    if (ret != 1) {
        return 0;
    }

    // Try to read the actual bytes read from the readbytes pointer
    size_t actual_read = 0;
    if (params->readbytes) {
        bpf_probe_read(&actual_read, sizeof(actual_read),
                       (const void *)params->readbytes);
    }

    LOG_TRACE("ssl_read_ex_exit: ctx=%p, actual_read=%d", (void *)params->ssl,
              actual_read);

    if (actual_read > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        LOG_DEBUG("ssl_read_ex_exit: buffer is too big: %d > %d", actual_read,
                  MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = params->ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
        LOG_TRACE("ssl_read_ex_exit: session not found");
        return 0;
    }

    __u8 http_version = HTTP_VERSION_UNKNOWN;
    __u8 http_message_type = HTTP_MESSAGE_UNKNOWN;
    if (session->http_version == HTTP_VERSION_UNKNOWN) {
        identify_http_version(ssl_ptr, (const char *)params->buf, ret,
                              &http_version, &http_message_type);

        if (http_version == HTTP_VERSION_UNKNOWN) {
            return 0;
        }

        // We only care about HTTP clients (not servers).
        // SSL_read_ex should be called only for responses.
        if (http_message_type == HTTP_MESSAGE_REQUEST) {
            return 0;
        }

        LOG_TRACE("ssl_read_ex_exit: http_version=%d, http_message_type=%d",
                  http_version, http_message_type);

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        LOG_WARN("ssl_read_ex_exit: failed to reserve ring buffer for "
                 "SSL_read_ex event");
        return 0;
    }

    event->header.event_type = EVENT_TLS_PAYLOAD_RECV;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->ssl_ctx = ssl_ptr;
    event->http_version = session->http_version;
    event->size = actual_read;
    event->buf_size = actual_read > MAX_BUF_SIZE ? MAX_BUF_SIZE : actual_read;

    if (bpf_probe_read(&event->buf, event->buf_size,
                       (const void *)params->buf) != 0) {
        LOG_WARN("ssl_read_ex_exit: failed to read SSL_read_ex data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);
    return 0;
}

SEC("uprobe/SSL_write_ex")
int BPF_UPROBE(ssl_write_ex_entry, void *ssl, const void *buf, size_t num,
               size_t *written) {
    if (num <= 0) {
        return 0;
    }

    if (num > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        LOG_DEBUG("ssl_write_ex_entry: buffer is too big: %d > %d", num,
                  MAX_BUF_SIZE);
    }

    LOG_TRACE("ssl_write_entry: ctx=%p, num=%d", ssl, num);

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = (__u64)ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
        LOG_TRACE("ssl_write_ex_entry: session not found");
        return 0;
    }

    __u8 http_version = HTTP_VERSION_UNKNOWN;
    __u8 http_message_type = HTTP_MESSAGE_UNKNOWN;
    if (session->http_version == HTTP_VERSION_UNKNOWN) {
        identify_http_version(ssl_ptr, buf, num, &http_version,
                              &http_message_type);

        if (http_version == HTTP_VERSION_UNKNOWN) {
            return 0;
        }

        LOG_TRACE("ssl_write_ex_entry: http_version=%d, http_message_type=%d",
                  http_version, http_message_type);

        // We only care about HTTP clients (not servers).
        // SSL_write_ex should be called only for requests.
        if (http_message_type == HTTP_MESSAGE_RESPONSE) {
            return 0;
        }

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        LOG_WARN("ssl_write_ex_entry: failed to reserve ring buffer for "
                 "SSL_write_ex event");
        return 0;
    }

    event->header.event_type = EVENT_TLS_PAYLOAD_SEND;
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    event->header.pid = pid;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->ssl_ctx = ssl_ptr;
    event->http_version = session->http_version;
    event->size = num;
    event->buf_size = num > MAX_BUF_SIZE ? MAX_BUF_SIZE : num;

    if (bpf_probe_read(&event->buf, event->buf_size, buf) != 0) {
        LOG_WARN("ssl_write_ex_entry: failed to read SSL_write_ex data");
        bpf_ringbuf_discard(event, 0);
        return 0;
    }

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// Track SSL session creation
SEC("uretprobe/SSL_new")
int BPF_URETPROBE(ssl_new_exit, void *ssl) {
    if (!ssl) {
        return 0;
    }

    LOG_TRACE("ssl_new_exit: ctx=%p", ssl);

    __u64 ssl_ptr = (__u64)ssl;
    struct ssl_session session = {
        .http_version = HTTP_VERSION_UNKNOWN,
        .is_active = 0,
    };

    bpf_map_update_elem(&ssl_sessions, &ssl_ptr, &session, BPF_ANY);
    return 0;
}

// Track SSL session destruction
SEC("uprobe/SSL_free")
int BPF_UPROBE(ssl_free_entry, void *ssl) {
    if (!ssl) {
        return 0;
    }

    LOG_TRACE("ssl_free_entry: ctx=%p", ssl);

    __u64 ssl_ptr = (__u64)ssl;
    bpf_map_delete_elem(&ssl_sessions, &ssl_ptr);

    struct tls_free_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_free_event), 0);
    if (!event) {
        LOG_WARN(
            "ssl_free_entry: failed to reserve ring buffer for SSL_free event");
        return 0;
    }

    event->header.event_type = EVENT_TLS_FREE;
    event->header.pid = bpf_get_current_pid_tgid() >> 32;
    bpf_get_current_comm(&event->header.comm, sizeof(event->header.comm));
    event->ssl_ctx = ssl_ptr;

    bpf_ringbuf_submit(event, 0);

    return 0;
}

// Track SSL handshake entry
SEC("uprobe/SSL_do_handshake")
int BPF_UPROBE(ssl_do_handshake_entry, void *ssl) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;
    __u64 ssl_ptr = (__u64)ssl;

    bpf_map_update_elem(&ssl_handshake_args, &pid, &ssl_ptr, BPF_ANY);
    return 0;
}

// Track SSL handshake completion
SEC("uretprobe/SSL_do_handshake")
int BPF_URETPROBE(ssl_do_handshake_exit, int ret) {
    __u32 pid = bpf_get_current_pid_tgid() >> 32;

    __u64 *ssl_ptr = bpf_map_lookup_elem(&ssl_handshake_args, &pid);
    if (!ssl_ptr) {
        return 0;
    }

    LOG_TRACE("ssl_do_handshake_exit: ctx=%p, ret=%d", *ssl_ptr, ret);

    __u64 ssl = *ssl_ptr;
    bpf_map_delete_elem(&ssl_handshake_args, &pid);

    // Handshake successful
    if (ret != 1) {
        return 0;
    }

    // Mark session as ready for data
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl);
    if (session) {
        session->is_active = 1;
        bpf_map_update_elem(&ssl_sessions, &ssl, session, BPF_ANY);
    }

    return 0;
}

// Clean up cache when inode is destroyed
SEC("fentry/destroy_inode")
int BPF_PROG(trace_destroy_inode, struct inode *inode) {
    if (!inode) {
        return 0;
    }

    __u32 inode_num = BPF_CORE_READ(inode, i_ino);
    bpf_map_delete_elem(&inode_process_map, &inode_num);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";