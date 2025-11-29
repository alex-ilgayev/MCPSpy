// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __HELPERS_H
#define __HELPERS_H

#include "types.h"
#include <bpf/bpf_core_read.h>

// Check if filename matches our criteria for TLS uprobe hook.
// Currently the options are:
// - "node"
// - "libssl.so*" (OpenSSL shared libraries - covers both 1.x and 3.x)
// Note: libssl3.so is NSS (Network Security Services), not OpenSSL, so we
// exclude it
static __always_inline bool is_filename_relevant(const char *filename) {
    // Check if filename is "node"
    if (filename[0] == 'n' && filename[1] == 'o' && filename[2] == 'd' &&
        filename[3] == 'e' && filename[4] == '\0') {
        return true;
    }

    // Check if filename is "libssl.so*" (OpenSSL libraries)
    // This matches libssl.so, libssl.so.1, libssl.so.3, etc.
    if (filename[0] == 'l' && filename[1] == 'i' && filename[2] == 'b' &&
        filename[3] == 's' && filename[4] == 's' && filename[5] == 'l' &&
        filename[6] == '.' && filename[7] == 's' && filename[8] == 'o' &&
        (filename[9] == '\0' || filename[9] == '.')) {
        return true;
    }

    return false;
}

// Filtering out non-interesting paths in linux,
// such as /proc, /sys, /dev, /mnt, /memfd.
// Used in FS open hook.
static __always_inline bool is_path_relevant(const char *path) {
    if (path[0] == '/' && path[1] == 'p' && path[2] == 'r' && path[3] == 'o' &&
        path[4] == 'c' && path[5] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 's' && path[2] == 'y' && path[3] == 's' &&
        path[4] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'd' && path[2] == 'e' && path[3] == 'v' &&
        path[4] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'm' && path[2] == 'n' && path[3] == 't' &&
        path[4] == '/') {
        return false;
    }

    if (path[0] == '/' && path[1] == 'm' && path[2] == 'e' && path[3] == 'm' &&
        path[4] == 'f') {
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

// Check if file's inode is a pipe (FIFO).
// This is used to filter stdio-based MCP communication which uses pipes.
static __always_inline bool is_pipe(struct file *file) {
    if (!file) {
        return false;
    }

    __u16 i_mode = BPF_CORE_READ(file, f_inode, i_mode);
    return (i_mode & S_IFMT) == S_IFIFO;
}

// Get the struct sock pointer from a file.
// Returns NULL if not a socket file or not readable.
static __always_inline struct sock *get_sock_from_file(struct file *file) {
    if (!file) {
        return NULL;
    }

    // Verify this is actually a socket file
    __u16 i_mode = BPF_CORE_READ(file, f_inode, i_mode);
    if ((i_mode & S_IFMT) != S_IFSOCK) {
        return NULL;
    }

    struct socket *sock = (struct socket *)BPF_CORE_READ(file, private_data);
    if (!sock) {
        return NULL;
    }

    return BPF_CORE_READ(sock, sk);
}

// Check if file is a Unix domain stream socket.
// Returns true if the file is a Unix socket with SOCK_STREAM type.
static __always_inline bool is_unix_socket(struct file *file) {
    struct sock *sk = get_sock_from_file(file);
    if (!sk) {
        return false;
    }

    // Check socket family
    __u16 family = BPF_CORE_READ(sk, __sk_common.skc_family);
    if (family != AF_UNIX) {
        return false;
    }

    // Check socket type (need socket struct for this)
    struct socket *sock = (struct socket *)BPF_CORE_READ(file, private_data);
    if (!sock) {
        return false;
    }

    __u16 type = BPF_CORE_READ(sock, type);
    return type == SOCK_STREAM;
}

// Check if file is an allowed IPC mechanism for MCP communication.
// Allowed types:
// - Pipes (S_IFIFO) - standard stdio transport
// - Unix domain sockets (AF_UNIX + SOCK_STREAM) - used by some clients like Claude
static __always_inline bool is_ipc_allowed(struct file *file) {
    return is_pipe(file) || is_unix_socket(file);
}

// Get the peer sock pointer for a Unix domain socket
// Returns NULL if no peer or not a Unix socket
// Note: This uses BPF CO-RE to read from unix_sock structure
static __always_inline struct sock *get_unix_peer_sock(struct sock *sk) {
    if (!sk) {
        return NULL;
    }

    // unix_sock embeds sock as its first member, so casting is safe
    // struct unix_sock { struct sock sk; ... struct sock *peer; }
    // We use CO-RE to read the peer field
    return BPF_CORE_READ((struct unix_sock *)sk, peer);
}

// Get the mount namespace ID of the current task
static __always_inline __u32 get_mount_ns_id(void) {
    struct task_struct *task = (struct task_struct *)bpf_get_current_task();
    if (!task) {
        return 0;
    }

    struct nsproxy *nsproxy = BPF_CORE_READ(task, nsproxy);
    if (!nsproxy) {
        return 0;
    }

    struct mnt_namespace *mnt_ns = BPF_CORE_READ(nsproxy, mnt_ns);
    if (!mnt_ns) {
        return 0;
    }

    return BPF_CORE_READ(mnt_ns, ns.inum);
}

// Get the mount namespace ID from a file's mount
static __always_inline __u32 get_file_mount_ns_id(struct file *file) {
    if (!file) {
        return 0;
    }

    struct vfsmount *vfs_mnt = BPF_CORE_READ(file, f_path.mnt);
    if (!vfs_mnt) {
        return 0;
    }

    // struct vfsmount is embedded in struct mount at field 'mnt'
    // Use CO-RE to calculate the offset and get to the parent structure
    struct mount *mnt = NULL;
    __builtin_preserve_access_index(
        ({ mnt = container_of(vfs_mnt, struct mount, mnt); }));

    if (!mnt) {
        return 0;
    }

    struct mnt_namespace *mnt_ns = BPF_CORE_READ(mnt, mnt_ns);
    if (!mnt_ns) {
        return 0;
    }

    return BPF_CORE_READ(mnt_ns, ns.inum);
}

// Check if the given PID belongs to the mcpspy process itself
// and should be ignored
static __always_inline bool should_ignore_pid(__u32 pid) {
    __u32 key = 0;
    __u32 *mcpspy_pid = bpf_map_lookup_elem(&mcpspy_pid_map, &key);

    // Check PID match (if set)
    if (mcpspy_pid && *mcpspy_pid != 0 && pid == *mcpspy_pid) {
        return true;
    }

    // Also check by comm name - handles cases where PID namespace differs
    // (e.g., WSL environments)
    char comm[TASK_COMM_LEN];
    bpf_get_current_comm(comm, sizeof(comm));

    // Check if comm starts with "mcpspy"
    if (comm[0] == 'm' && comm[1] == 'c' && comm[2] == 'p' &&
        comm[3] == 's' && comm[4] == 'p' && comm[5] == 'y') {
        return true;
    }

    return false;
}

#endif // __HELPERS_H