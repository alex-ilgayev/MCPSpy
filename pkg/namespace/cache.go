package namespace

import (
	"fmt"
	"sync"
	"syscall"
)

// namespaceCache caches namespace file descriptors to avoid repeated lookups
type namespaceCache struct {
	fds map[uint64]int // nsID -> fd
	mu  sync.RWMutex
}

// newNamespaceCache creates a new namespace cache
func newNamespaceCache() *namespaceCache {
	return &namespaceCache{
		fds: make(map[uint64]int),
	}
}

// get returns a cached file descriptor for the given namespace ID
func (nc *namespaceCache) get(nsID uint64) (int, bool) {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	fd, exists := nc.fds[nsID]
	return fd, exists
}

// set stores a file descriptor for the given namespace ID
func (nc *namespaceCache) set(nsID uint64, fd int) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	nc.fds[nsID] = fd
}

// remove removes a namespace from the cache and closes its file descriptor
func (nc *namespaceCache) remove(nsID uint64) error {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	if fd, exists := nc.fds[nsID]; exists {
		delete(nc.fds, nsID)
		if err := syscall.Close(fd); err != nil {
			return fmt.Errorf("failed to close fd for namespace %d: %w", nsID, err)
		}
	}
	return nil
}

// close closes all cached file descriptors
func (nc *namespaceCache) close() error {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	var lastErr error
	for nsID, fd := range nc.fds {
		if err := syscall.Close(fd); err != nil {
			lastErr = fmt.Errorf("failed to close fd for namespace %d: %w", nsID, err)
		}
	}

	nc.fds = make(map[uint64]int)
	return lastErr
}

// validateAndCleanCache checks all cached file descriptors and removes invalid ones
func (nc *namespaceCache) validateAndCleanCache() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	toRemove := make([]uint64, 0)
	
	// First, identify invalid fds
	for nsID, fd := range nc.fds {
		if !IsValidNamespaceFd(fd) {
			toRemove = append(toRemove, nsID)
		}
	}
	
	// Then remove them (without calling remove() to avoid mutex deadlock)
	for _, nsID := range toRemove {
		if fd, exists := nc.fds[nsID]; exists {
			delete(nc.fds, nsID)
			syscall.Close(fd) // Close the invalid fd
		}
	}
}
