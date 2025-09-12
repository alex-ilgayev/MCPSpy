package namespace

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var cleanupInterval = 30 * time.Second // Default cleanup interval for cache

// GetCurrentMountNamespace returns the mount namespace ID of the current process
func GetCurrentMountNamespace() (uint64, error) {
	return GetMountNamespace(os.Getpid())
}

// GetMountNamespace returns the mount namespace ID for the given process ID
func GetMountNamespace(pid int) (uint64, error) {
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
	nsID, err := strconv.ParseUint(nsIDStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse namespace ID: %w", err)
	}

	return nsID, nil
}

// IsValidNamespaceFd tests if a file descriptor is still valid for namespace operations
func IsValidNamespaceFd(fd int) bool {
	// We'll test the fd by checking if we can read the namespace link
	// This is safer than actually calling setns which could affect the current thread

	// Try to read the file descriptor as a link to get namespace info
	// We use unix.Fstat to check if the fd is still valid
	var stat unix.Stat_t
	err := unix.Fstat(fd, &stat)
	return err == nil
}

// MountNamespaceSwitcher provides functionality to temporarily switch mount namespaces
type MountNamespaceSwitcher struct {
	originalFd    int
	cache         *namespaceCache
	cleanupCancel context.CancelFunc
	cleanupDone   chan struct{}
}

// NewMountNamespaceSwitcher creates a new mount namespace switcher and saves the current namespace
func NewMountNamespaceSwitcher() (*MountNamespaceSwitcher, error) {
	// Open current mount namespace to restore later
	originalFd, err := syscall.Open("/proc/self/ns/mnt", syscall.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open current mount namespace: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ms := &MountNamespaceSwitcher{
		originalFd:    originalFd,
		cache:         newNamespaceCache(),
		cleanupCancel: cancel,
		cleanupDone:   make(chan struct{}),
	}

	logrus.WithField("fd", originalFd).Trace("MountNamespaceSwitcher created")

	// Start background cleanup goroutine
	go ms.backgroundCleanup(ctx)

	return ms, nil
}

// backgroundCleanup periodically validates cached file descriptors and removes invalid ones
func (ms *MountNamespaceSwitcher) backgroundCleanup(ctx context.Context) {
	defer close(ms.cleanupDone)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ms.cache.validateAndCleanCache()
		}
	}
}

// SwitchTo switches to the specified mount namespace using cached file descriptors
func (ms *MountNamespaceSwitcher) SwitchTo(targetNSID uint32) error {
	logrus.WithField("nsID", targetNSID).Trace("Switching to mount namespace")

	nsID := uint64(targetNSID)

	// Try to get cached file descriptor first
	if fd, exists := ms.cache.get(nsID); exists {
		logrus.WithField("fd", fd).Trace("Found cached fd for namespace, attempting switch")
		err := ms.SwitchToByFd(fd)
		if err == nil {
			return nil // Success with cached fd
		}

		logrus.WithError(err).WithField("fd", fd).Warn("Failed to switch using cached fd. Invalidating cache.")

		// Cache might be stale, remove it and try manual discovery
		ms.cache.remove(nsID)
	}

	// Manual discovery: find and open the namespace file
	logrus.WithField("nsID", nsID).Trace("Attempting to find namespace by scanning /proc")
	fd, err := ms.findNamespaceByID(nsID)
	if err != nil {
		return fmt.Errorf("failed to find namespace %d: %w", targetNSID, err)
	}

	// Switch using the newly found fd
	logrus.WithField("fd", fd).Trace("Found namespace fd, attempting switch")
	err = ms.SwitchToByFd(fd)
	if err != nil {
		syscall.Close(fd) // Close fd if switch failed
		return fmt.Errorf("failed to switch to namespace %d: %w", targetNSID, err)
	}

	// Cache the newly found fd
	ms.cache.set(nsID, fd)

	return nil
}

// findNamespaceByID finds and opens a namespace file for the given ID
func (ms *MountNamespaceSwitcher) findNamespaceByID(targetNSID uint64) (int, error) {
	procDir, err := os.Open("/proc")
	if err != nil {
		return -1, fmt.Errorf("failed to open /proc: %w", err)
	}
	defer procDir.Close()

	entries, err := procDir.Readdirnames(-1)
	if err != nil {
		return -1, fmt.Errorf("failed to read /proc entries: %w", err)
	}

	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry); err == nil {
			nsID, err := GetMountNamespace(pid)
			if err != nil {
				continue
			}
			if nsID == targetNSID {
				nsPath := fmt.Sprintf("/proc/%d/ns/mnt", pid)
				fd, err := syscall.Open(nsPath, syscall.O_RDONLY, 0)
				if err != nil {
					continue
				}
				return fd, nil
			}
		}
	}

	return -1, fmt.Errorf("no process found in mount namespace %d", targetNSID)
}

// SwitchToByFd switches to the mount namespace using a pre-opened file descriptor
func (ms *MountNamespaceSwitcher) SwitchToByFd(nsFd int) error {
	// Lock OS thread to ensure namespace change stays within this goroutine
	runtime.LockOSThread()

	// Switch to target namespace using setns syscall
	_, _, errno := syscall.Syscall(unix.SYS_SETNS, uintptr(nsFd), uintptr(unix.CLONE_NEWNS), 0)
	if errno != 0 {
		runtime.UnlockOSThread()
		return fmt.Errorf("failed to switch to mount namespace using fd %d: %v", nsFd, errno)
	}

	return nil
}

// Restore restores the original mount namespace and unlocks the OS thread
func (ms *MountNamespaceSwitcher) Restore() error {
	logrus.Trace("Restoring original mount namespace")

	defer runtime.UnlockOSThread()

	// Restore original namespace
	_, _, errno := syscall.Syscall(unix.SYS_SETNS, uintptr(ms.originalFd), uintptr(unix.CLONE_NEWNS), 0)
	if errno != 0 {
		return fmt.Errorf("failed to restore original mount namespace: %v", errno)
	}

	return nil
}

// Close closes the switcher and its resources
func (ms *MountNamespaceSwitcher) Close() error {
	// Stop background cleanup
	if ms.cleanupCancel != nil {
		ms.cleanupCancel()
		<-ms.cleanupDone // Wait for cleanup goroutine to finish
	}

	var lastErr error

	if err := syscall.Close(ms.originalFd); err != nil {
		lastErr = fmt.Errorf("failed to close original fd: %w", err)
	}

	if ms.cache != nil {
		if err := ms.cache.close(); err != nil {
			lastErr = fmt.Errorf("failed to close cache: %w", err)
		}
	}

	return lastErr
}
