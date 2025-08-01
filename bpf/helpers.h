// SPDX-License-Identifier: GPL-2.0-only
// go:build ignore

#ifndef __HELPERS_H
#define __HELPERS_H

#include "types.h"

// Check if filename matches our criteria for TLS uprobe hook.
// Currently the options are:
// - "node"
// - "libssl"
static __always_inline bool is_filename_relevant(const char *filename) {
    // Check if filename is "node"
    if (filename[0] == 'n' && filename[1] == 'o' && filename[2] == 'd' &&
        filename[3] == 'e' && filename[4] == '\0') {
        return true;
    }

    // Check if filename starts with "libssl"
    if (filename[0] == 'l' && filename[1] == 'i' && filename[2] == 'b' &&
        filename[3] == 's' && filename[4] == 's' && filename[5] == 'l') {
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

#endif // __HELPERS_H