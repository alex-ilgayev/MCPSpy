// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#include "args.h"
#include "helpers.h"
#include "json.h"
#include "tls.h"
#include "types.h"
#include "vmlinux.h"
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_helpers.h>

SEC("fexit/vfs_read")
int BPF_PROG(exit_vfs_read, struct file *file, const char *buf, size_t count,
             loff_t *_pos, ssize_t ret) {
    if (ret <= 0) {
        // We logging only operations with data.
        return 0;
    }

    // Create stream key
    struct stream_key key = {
        .pid = bpf_get_current_pid_tgid() >> 32,
        .file_ptr = (__u64)file,
    };

    // Lookup existing aggregation state
    struct json_aggregation_state *state =
        bpf_map_lookup_elem(&json_streams, &key);

    if (!state) {
        // New stream - validate it starts with valid JSON
        if (!is_json_data(buf, ret)) {
            return 0;
        }

        // Use scratch space to avoid stack overflow (indexed by CPU ID)
        // Array is pre-allocated and zero-initialized by kernel
        __u32 cpu_id = bpf_get_smp_processor_id();
        struct json_aggregation_state *new_state =
            bpf_map_lookup_elem(&json_scratch, &cpu_id);
        if (!new_state) {
            bpf_printk("error: failed to get scratch space for cpu %d", cpu_id);
            return 0;
        }

        // Initialize metadata
        new_state->accumulated_size = 0;
        new_state->open_brackets = 0;
        new_state->close_brackets = 0;
        new_state->found_opening = true;
        new_state->operation = EVENT_READ;
        new_state->last_update_ns = bpf_ktime_get_ns();

        // Copy initial buffer
        if (append_to_aggregation(new_state, buf, ret) != 0) {
            bpf_printk("error: failed to append initial buffer to new state");
            return 0;
        }

        // Count brackets in initial buffer
        update_bracket_counts(new_state, buf, ret);

        // Check if complete in single buffer
        if (is_json_complete(new_state)) {
            // Submit immediately
            submit_json_event(&key, new_state);
            return 0;
        }

        // Store state for continuation
        bpf_map_update_elem(&json_streams, &key, new_state, BPF_ANY);
        return 0;
    }

    // Existing stream - append new buffer
    if (append_to_aggregation(state, buf, ret) != 0) {
        bpf_printk("warn: buffer overflow (>64KB), dropping stream pid=%d",
                   key.pid);
        bpf_map_delete_elem(&json_streams, &key);
        return 0;
    }

    // Update bracket counts
    update_bracket_counts(state, buf, ret);
    state->last_update_ns = bpf_ktime_get_ns();

    // Update the map with new state
    bpf_map_update_elem(&json_streams, &key, state, BPF_EXIST);

    // Check if complete
    if (is_json_complete(state)) {
        submit_json_event(&key, state);
        bpf_map_delete_elem(&json_streams, &key);
    }

    return 0;
}

SEC("fexit/vfs_write")
int BPF_PROG(exit_vfs_write, struct file *file, const char *buf, size_t count,
             loff_t *_pos, size_t ret) {
    if (ret <= 0) {
        // We logging only operations with data.
        return 0;
    }

    // // Create stream key
    // struct stream_key key = {
    //     .pid = bpf_get_current_pid_tgid() >> 32,
    //     .file_ptr = (__u64)file,
    // };

    // // Lookup existing aggregation state
    // struct json_aggregation_state *state = bpf_map_lookup_elem(&json_streams,
    // &key);

    // if (!state) {
    //     // New stream - validate it starts with valid JSON
    //     if (!is_json_data(buf, ret)) {
    //         return 0;
    //     }

    //     // Use scratch space to avoid stack overflow (indexed by CPU ID)
    //     // Array is pre-allocated and zero-initialized by kernel
    //     __u32 cpu_id = bpf_get_smp_processor_id();
    //     struct json_aggregation_state *new_state =
    //     bpf_map_lookup_elem(&json_scratch, &cpu_id); if (!new_state) {
    //         bpf_printk("error: failed to get scratch space for cpu %d",
    //         cpu_id); return 0;
    //     }

    //     // Initialize metadata
    //     new_state->accumulated_size = 0;
    //     new_state->open_brackets = 0;
    //     new_state->close_brackets = 0;
    //     new_state->found_opening = true;
    //     new_state->operation = EVENT_WRITE;
    //     new_state->last_update_ns = bpf_ktime_get_ns();

    //     // Copy initial buffer
    //     if (append_to_aggregation(new_state, buf, ret) != 0) {
    //         bpf_printk("error: failed to append initial buffer to new
    //         state"); return 0;
    //     }

    //     // Count brackets in initial buffer
    //     update_bracket_counts(new_state, buf, ret);

    //     // Check if complete in single buffer
    //     if (is_json_complete(new_state)) {
    //         // Submit immediately
    //         submit_json_event(&key, new_state);
    //         return 0;
    //     }

    //     // Store state for continuation
    //     bpf_map_update_elem(&json_streams, &key, new_state, BPF_ANY);
    //     return 0;
    // }

    // // Existing stream - append new buffer
    // if (append_to_aggregation(state, buf, ret) != 0) {
    //     bpf_printk("warn: buffer overflow (>64KB), dropping stream pid=%d",
    //     key.pid); bpf_map_delete_elem(&json_streams, &key); return 0;
    // }

    // // Update bracket counts
    // update_bracket_counts(state, buf, ret);
    // state->last_update_ns = bpf_ktime_get_ns();

    // // Update the map with new state
    // bpf_map_update_elem(&json_streams, &key, state, BPF_EXIST);

    // // Check if complete
    // if (is_json_complete(state)) {
    //     submit_json_event(&key, state);
    //     bpf_map_delete_elem(&json_streams, &key);
    // }

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
        bpf_printk("error: failed to reserve ring buffer for library event");
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
        bpf_printk("error: failed to reserve ring buffer for security file "
                   "open event");
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

    if (ret > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        bpf_printk("info: ssl_read_exit: buffer is too big: %d > %d", ret,
                   MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = params->ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
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
        bpf_printk("error: failed to reserve ring buffer for SSL_read event");
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

    if (num > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        bpf_printk("info: ssl_write_entry: buffer is too big: %d > %d", num,
                   MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = (__u64)ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
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

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_write event");
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
        bpf_printk("error: failed to read SSL_write data");
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

    if (actual_read > MAX_BUF_SIZE) {
        // We still want to deliver these messages for HTTP session integrity.
        // But it means we'll may lose information.
        bpf_printk("info: ssl_read_ex_exit: buffer is too big: %d > %d",
                   actual_read, MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = params->ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
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

        session->http_version = http_version;
        bpf_map_update_elem(&ssl_sessions, &ssl_ptr, session, BPF_ANY);
    }

    struct tls_payload_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_payload_event), 0);
    if (!event) {
        bpf_printk(
            "error: failed to reserve ring buffer for SSL_read_ex event");
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
        bpf_printk("error: failed to read SSL_read_ex data");
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
        bpf_printk("info: ssl_write_ex_entry: buffer is too big: %d > %d", num,
                   MAX_BUF_SIZE);
    }

    // Checking the session if was set to specific http version.
    // If not, we try to identify the version from the payload.
    __u64 ssl_ptr = (__u64)ssl;
    struct ssl_session *session = bpf_map_lookup_elem(&ssl_sessions, &ssl_ptr);
    if (!session) {
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
        bpf_printk(
            "error: failed to reserve ring buffer for SSL_write_ex event");
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
        bpf_printk("error: failed to read SSL_write_ex data");
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

    __u64 ssl_ptr = (__u64)ssl;
    bpf_map_delete_elem(&ssl_sessions, &ssl_ptr);

    struct tls_free_event *event =
        bpf_ringbuf_reserve(&events, sizeof(struct tls_free_event), 0);
    if (!event) {
        bpf_printk("error: failed to reserve ring buffer for SSL_free event");
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

char LICENSE[] SEC("license") = "GPL";