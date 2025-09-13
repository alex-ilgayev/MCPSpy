package namespace

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// GetCurrentMountNamespace returns the mount namespace ID of the current process
func GetCurrentMountNamespace() (uint32, error) {
	return GetMountNamespace(os.Getpid())
}

// GetMountNamespace returns the mount namespace ID for the given process ID
func GetMountNamespace(pid int) (uint32, error) {
	nsPath := fmt.Sprintf("/proc/%d/ns/mnt", pid)

	target, err := os.Readlink(nsPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read mount namespace link: %w", err)
	}

	// Extract namespace ID from format like "mnt:[4026531840]"
	if !strings.HasPrefix(target, "mnt:[") || !strings.HasSuffix(target, "]") {
		return 0, fmt.Errorf("unexpected namespace link format: %s", target)
	}

	nsIDStr := target[5 : len(target)-1] // Remove "mnt:[" and "]"
	nsID, err := strconv.ParseUint(nsIDStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("failed to parse namespace ID: %w", err)
	}

	return uint32(nsID), nil
}

// GetPathInMountNamespace finds a process in the specified mount namespace and constructs
// the path relative to that process's root filesystem
// While it's not the best solution, and may be error-prone, currently it is an easy way
// to support when the host doesn't see the entire process filesystem (e.g. inside a container)
func GetPathInMountNamespace(path string, mntNamespaceID uint32) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path must be absolute")
	}

	// Read all entries in /proc
	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return "", fmt.Errorf("failed to read /proc directory: %w", err)
	}

	// Iterate through all process directories
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if directory name is a PID (numeric)
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			// Skip non-numeric directories
			continue
		}

		// Get the mount namespace for this process
		procMntNS, err := GetMountNamespace(pid)
		if err != nil {
			// Skip processes we can't access or that have errors
			continue
		}

		// Check if this process is in the target mount namespace
		if procMntNS == mntNamespaceID {
			// Found a process in the target mount namespace
			// Construct the path using this process's root
			return fmt.Sprintf("/proc/%d/root%s", pid, path), nil
		}
	}

	return "", fmt.Errorf("no process found in mount namespace %d", mntNamespaceID)
}
