package namespace

import (
	"strings"
	"syscall"
	"testing"
)

func TestNamespaceCache_GetAndSet(t *testing.T) {
	cache := newNamespaceCache()
	defer cache.close()

	nsID := uint64(12345)
	fd := 10

	// Initially should not exist
	_, exists := cache.get(nsID)
	if exists {
		t.Error("Expected namespace to not exist in cache initially")
	}

	// Set the fd
	cache.set(nsID, fd)

	// Should now exist and return the same fd
	cachedFd, exists := cache.get(nsID)
	if !exists {
		t.Error("Expected namespace to exist in cache after setting")
	}

	if cachedFd != fd {
		t.Errorf("Expected cached fd to be %d, got %d", fd, cachedFd)
	}
}

func TestNamespaceCache_Remove(t *testing.T) {
	cache := newNamespaceCache()
	defer cache.close()

	nsID := uint64(12345)
	fd := 10

	// Set the fd
	cache.set(nsID, fd)

	// Verify it exists
	_, exists := cache.get(nsID)
	if !exists {
		t.Error("Expected namespace to exist in cache after setting")
	}

	// Remove it (note: this test will error when trying to close fake fd, but that's expected)
	err := cache.remove(nsID)
	if err == nil {
		t.Log("Remove succeeded (unexpected with fake fd, but OK)")
	} else if !strings.Contains(err.Error(), "bad file descriptor") {
		t.Errorf("Expected 'bad file descriptor' error or no error, got: %v", err)
	}

	// Should no longer exist in cache regardless of close error
	_, exists = cache.get(nsID)
	if exists {
		t.Error("Expected namespace to not exist in cache after removal")
	}
}

func TestNamespaceCache_Close(t *testing.T) {
	cache := newNamespaceCache()

	nsID := uint64(12345)
	fd := 10

	// Set some fake fds to populate cache
	cache.set(nsID, fd)
	cache.set(nsID+1, fd+1)

	// Verify cache has entries
	if len(cache.fds) != 2 {
		t.Errorf("Expected 2 entries in cache, got %d", len(cache.fds))
	}

	// Close should work (note: this will try to close fake fds, which may error)
	err := cache.close()
	// We expect errors since we're closing fake fds, but the cache should still be cleared
	if err == nil {
		t.Log("Close succeeded (unexpected with fake fds, but OK)")
	}

	// Cache should be empty after close
	if len(cache.fds) != 0 {
		t.Errorf("Expected empty cache after close, got %d entries", len(cache.fds))
	}
}

func TestNamespaceCache_ValidateAndCleanCache(t *testing.T) {
	cache := newNamespaceCache()
	defer cache.close()

	// Add a valid fd to cache
	currentFd, err := syscall.Open("/proc/self/ns/mnt", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open current namespace: %v", err)
	}
	defer syscall.Close(currentFd)

	currentNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get current namespace: %v", err)
	}

	cache.set(currentNS, currentFd)

	// Add an invalid fd to cache
	invalidFd := -1
	cache.set(99999, invalidFd)

	// Verify both entries exist
	if len(cache.fds) != 2 {
		t.Errorf("Expected 2 entries in cache before cleanup, got %d", len(cache.fds))
	}

	// Run validation and cleanup
	cache.validateAndCleanCache()

	// The invalid fd should be removed, valid one should remain
	if len(cache.fds) != 1 {
		t.Errorf("Expected 1 entry in cache after cleanup, got %d", len(cache.fds))
	}

	// The valid entry should still be there
	if _, exists := cache.get(currentNS); !exists {
		t.Error("Expected valid namespace to remain in cache")
	}

	// The invalid entry should be gone
	if _, exists := cache.get(99999); exists {
		t.Error("Expected invalid namespace to be removed from cache")
	}
}
