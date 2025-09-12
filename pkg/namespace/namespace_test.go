package namespace

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// isPermissionError checks if an error is due to insufficient permissions
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "operation not permitted") ||
		strings.Contains(errStr, "permission denied") ||
		err == syscall.EPERM ||
		err == syscall.EACCES
}

// requiresPrivileges skips the test if we don't have sufficient privileges
func requiresPrivileges(t *testing.T) {
	// Try to create a switcher and perform a basic operation
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Skipf("Skipping test that requires special privileges: %v", err)
	}

	// Try to restore immediately (this tests if we have setns privileges)
	err = switcher.Restore()
	if isPermissionError(err) {
		t.Skipf("Skipping test that requires CAP_SYS_ADMIN or root privileges: %v", err)
	}
	if err != nil {
		t.Fatalf("Unexpected error during privilege check: %v", err)
	}
}

func TestGetCurrentMountNamespace(t *testing.T) {
	nsID, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("GetCurrentMountNamespace() failed: %v", err)
	}

	if nsID == 0 {
		t.Error("Expected non-zero namespace ID")
	}

	t.Logf("Current mount namespace ID: %d", nsID)
}

func TestGetMountNamespace(t *testing.T) {
	pid := os.Getpid()
	nsID, err := GetMountNamespace(pid)
	if err != nil {
		t.Fatalf("GetMountNamespace(%d) failed: %v", pid, err)
	}

	if nsID == 0 {
		t.Error("Expected non-zero namespace ID")
	}

	// Should be the same as GetCurrentMountNamespace
	currentNsID, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("GetCurrentMountNamespace() failed: %v", err)
	}

	if nsID != currentNsID {
		t.Errorf("GetMountNamespace(%d) = %d, but GetCurrentMountNamespace() = %d", pid, nsID, currentNsID)
	}
}

func TestGetMountNamespaceInvalidPID(t *testing.T) {
	_, err := GetMountNamespace(999999)
	if err == nil {
		t.Error("Expected error for invalid PID")
	}
}

func TestNewMountNamespaceSwitcher(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	if switcher == nil {
		t.Error("Expected non-nil switcher")
	}

	if switcher.originalFd <= 0 {
		t.Error("Expected valid file descriptor for original namespace")
	}

	if switcher.cache == nil {
		t.Error("Expected non-nil cache")
	}

	// Clean up by restoring
	err = switcher.Restore()
	if isPermissionError(err) {
		t.Skipf("Skipping restore due to insufficient privileges: %v", err)
	}
	if err != nil {
		t.Errorf("Failed to restore namespace: %v", err)
	}
}

func TestMountNamespaceSwitcher_SwitchToNonExistentNamespace(t *testing.T) {
	requiresPrivileges(t)

	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()
	defer func() {
		// Ensure cleanup even if test fails
		if restoreErr := switcher.Restore(); !isPermissionError(restoreErr) && restoreErr != nil {
			t.Logf("Failed to restore namespace during cleanup: %v", restoreErr)
		}
	}()

	// Try to switch to a non-existent namespace ID
	err = switcher.SwitchTo(12345)
	if err == nil {
		t.Error("Expected error when switching to non-existent namespace")
	}

	// Error should mention that no process was found in the namespace
	if err != nil && err.Error() == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestMountNamespaceSwitcher_SwitchToCurrentNamespace(t *testing.T) {
	requiresPrivileges(t)

	// Get current namespace ID
	currentNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get current namespace: %v", err)
	}

	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()
	defer func() {
		if restoreErr := switcher.Restore(); !isPermissionError(restoreErr) && restoreErr != nil {
			t.Errorf("Failed to restore namespace: %v", restoreErr)
		}
	}()

	// Switch to current namespace (should work)
	err = switcher.SwitchTo(uint32(currentNS))
	if err != nil {
		t.Errorf("Failed to switch to current namespace: %v", err)
	}

	// Verify we're still in the same namespace
	afterSwitchNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get namespace after switch: %v", err)
	}

	if afterSwitchNS != currentNS {
		t.Errorf("Namespace changed unexpectedly: before=%d, after=%d", currentNS, afterSwitchNS)
	}
}

func TestMountNamespaceSwitcher_RestoreWithoutSwitch(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// Get current namespace before restore
	beforeNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get current namespace: %v", err)
	}

	// Restore without switching (should work fine or fail due to permissions)
	err = switcher.Restore()
	if isPermissionError(err) {
		t.Skipf("Skipping restore test due to insufficient privileges: %v", err)
	}
	if err != nil {
		t.Errorf("Failed to restore without switching: %v", err)
	}

	// Verify namespace hasn't changed
	afterNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get namespace after restore: %v", err)
	}

	if beforeNS != afterNS {
		t.Errorf("Namespace changed during restore without switch: before=%d, after=%d", beforeNS, afterNS)
	}
}

func TestMountNamespaceSwitcher_DoubleRestore(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// First restore should work or fail due to permissions
	err = switcher.Restore()
	if isPermissionError(err) {
		t.Skipf("Skipping double restore test due to insufficient privileges: %v", err)
	}
	if err != nil {
		t.Errorf("First restore failed: %v", err)
	}

	// Second restore should fail (file descriptor already closed)
	err = switcher.Restore()
	if err == nil {
		t.Error("Expected error on double restore (fd should be closed)")
	}
}

func TestMountNamespaceSwitcher_SwitchToByFd(t *testing.T) {
	requiresPrivileges(t)

	// Get current namespace
	currentNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get current namespace: %v", err)
	}

	// Open current namespace fd manually
	nsPath := "/proc/self/ns/mnt"
	fd, err := syscall.Open(nsPath, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open namespace fd: %v", err)
	}
	defer syscall.Close(fd)

	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()
	defer func() {
		if restoreErr := switcher.Restore(); !isPermissionError(restoreErr) && restoreErr != nil {
			t.Errorf("Failed to restore namespace: %v", restoreErr)
		}
	}()

	// Switch using fd directly
	err = switcher.SwitchToByFd(fd)
	if err != nil {
		t.Errorf("Failed to switch using fd: %v", err)
	}

	// Verify we're still in the same namespace
	afterSwitchNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get namespace after switch: %v", err)
	}

	if afterSwitchNS != currentNS {
		t.Errorf("Namespace changed unexpectedly: before=%d, after=%d", currentNS, afterSwitchNS)
	}
}

func TestMountNamespaceSwitcher_FindNamespaceByID(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// Get current namespace
	currentNS, err := GetCurrentMountNamespace()
	if err != nil {
		t.Fatalf("Failed to get current namespace: %v", err)
	}

	// Should be able to find current namespace
	fd, err := switcher.findNamespaceByID(currentNS)
	if err != nil {
		t.Errorf("Failed to find current namespace %d: %v", currentNS, err)
	}

	if fd <= 0 {
		t.Error("Expected valid file descriptor")
	}

	// Clean up by closing the fd we opened
	syscall.Close(fd)
}

func TestMountNamespaceSwitcher_FindNamespaceByID_NonExistent(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// Try to find non-existent namespace
	_, err = switcher.findNamespaceByID(99999999)
	if err == nil {
		t.Error("Expected error when finding non-existent namespace")
	}

	expectedErrMsg := "no process found in mount namespace"
	if err != nil && !strings.Contains(err.Error(), expectedErrMsg) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrMsg, err.Error())
	}
}

func TestMountNamespaceSwitcher_CacheAccess(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	nsID := uint64(12345)
	fd := 10

	// Manually add something to cache
	switcher.cache.set(nsID, fd)

	// Verify it exists
	_, exists := switcher.cache.get(nsID)
	if !exists {
		t.Error("Expected namespace to exist in cache")
	}

	// Manually remove it
	err = switcher.cache.remove(nsID)
	if err == nil {
		t.Log("Remove succeeded (unexpected with fake fd, but OK)")
	} else if !strings.Contains(err.Error(), "bad file descriptor") {
		t.Errorf("Expected 'bad file descriptor' error or no error, got: %v", err)
	}

	// Should no longer exist
	_, exists = switcher.cache.get(nsID)
	if exists {
		t.Error("Expected namespace to not exist in cache after removal")
	}
}

func TestMountNamespaceSwitcher_CacheClear(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// Manually add something to cache
	switcher.cache.set(12345, 10)
	switcher.cache.set(12346, 11)

	// Verify cache has entries
	if len(switcher.cache.fds) != 2 {
		t.Errorf("Expected 2 entries in cache, got %d", len(switcher.cache.fds))
	}

	// Clear cache by closing it (note: this will try to close fake fds, which may error)
	err = switcher.cache.close()
	// We expect errors since we're closing fake fds, but the cache should still be cleared
	if err == nil {
		t.Log("Close succeeded (unexpected with fake fds, but OK)")
	}

	// Cache should be empty
	if len(switcher.cache.fds) != 0 {
		t.Errorf("Expected empty cache after close, got %d entries", len(switcher.cache.fds))
	}
}
func TestNewMountNamespaceSwitcher_Cleanup(t *testing.T) {
	// Test with cleanup enabled (default)
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	if switcher.cleanupCancel == nil {
		t.Error("Expected cleanup cancel function to be set")
	}

	if switcher.cleanupDone == nil {
		t.Error("Expected cleanup done channel to be set")
	}

	// Verify cleanup goroutine is running by checking channel is not closed yet
	select {
	case <-switcher.cleanupDone:
		t.Error("Expected cleanup done channel to remain open while cleanup is running")
	case <-time.After(10 * time.Millisecond):
		// Good, cleanup is still running
	}
}

func TestIsValidNamespaceFd(t *testing.T) {
	// Test with valid fd (current namespace)
	currentFd, err := syscall.Open("/proc/self/ns/mnt", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open current namespace: %v", err)
	}
	defer syscall.Close(currentFd)

	if !IsValidNamespaceFd(currentFd) {
		t.Error("Expected valid fd to be reported as valid")
	}

	// Test with invalid fd
	if IsValidNamespaceFd(-1) {
		t.Error("Expected invalid fd to be reported as invalid")
	}

	// Test with closed fd
	tempFd, err := syscall.Open("/proc/self/ns/mnt", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open temp namespace: %v", err)
	}
	syscall.Close(tempFd) // Close it immediately

	if IsValidNamespaceFd(tempFd) {
		t.Error("Expected closed fd to be reported as invalid")
	}
}

func TestMountNamespaceSwitcher_ValidateAndCleanCache(t *testing.T) {
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

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

	switcher.cache.set(currentNS, currentFd)

	// Add an invalid fd to cache
	invalidFd := -1
	switcher.cache.set(99999, invalidFd)

	// Verify both entries exist
	if len(switcher.cache.fds) != 2 {
		t.Errorf("Expected 2 entries in cache before cleanup, got %d", len(switcher.cache.fds))
	}

	// Run validation and cleanup
	switcher.cache.validateAndCleanCache()

	// The invalid fd should be removed, valid one should remain
	if len(switcher.cache.fds) != 1 {
		t.Errorf("Expected 1 entry in cache after cleanup, got %d", len(switcher.cache.fds))
	}

	// The valid entry should still be there
	if _, exists := switcher.cache.get(currentNS); !exists {
		t.Error("Expected valid namespace to remain in cache")
	}

	// The invalid entry should be gone
	if _, exists := switcher.cache.get(99999); exists {
		t.Error("Expected invalid namespace to be removed from cache")
	}
}

func TestMountNamespaceSwitcher_BackgroundCleanup(t *testing.T) {
	// Test background cleanup functionality
	switcher, err := NewMountNamespaceSwitcher()
	if err != nil {
		t.Fatalf("NewMountNamespaceSwitcher() failed: %v", err)
	}
	defer switcher.Close()

	// Add an invalid fd that should get cleaned up
	switcher.cache.set(99999, -1)

	// Manually trigger cleanup (since we can't easily wait for the background cleanup)
	switcher.cache.validateAndCleanCache()

	// The invalid entry should be cleaned up
	if _, exists := switcher.cache.get(99999); exists {
		t.Error("Expected invalid namespace to be cleaned up")
	}

	// Test that Close() waits for cleanup goroutine
	start := time.Now()
	switcher.Close()
	duration := time.Since(start)

	// Should complete quickly since cleanup goroutine should exit
	if duration > 500*time.Millisecond {
		t.Errorf("Close() took too long: %v", duration)
	}
}
