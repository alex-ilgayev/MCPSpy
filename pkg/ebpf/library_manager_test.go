package ebpf

import (
	"errors"
	"testing"

	tu "github.com/alex-ilgayev/mcpspy/internal/testing"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// testLoader is a test implementation that tracks attach calls
type testLoader struct {
	attachCalls   []string
	attachResults map[string]error // path -> error
}

func newTestLoader() *testLoader {
	return &testLoader{
		attachCalls:   []string{},
		attachResults: make(map[string]error),
	}
}

func (t *testLoader) AttachSSLProbes(libraryPath string) error {
	t.attachCalls = append(t.attachCalls, libraryPath)
	if err, ok := t.attachResults[libraryPath]; ok {
		return err
	}
	return nil
}

func TestLibraryManager_ProcessLibraryEvent(t *testing.T) {
	// Create a test loader
	tl := newTestLoader()

	// Create library manager with the test loader
	lm, err := NewLibraryManager(tu.NewMockBus(), tl, 4026532221) // Use a test mount namespace
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Test successful hook
	t.Run("successful hook", func(t *testing.T) {
		event := &event.LibraryEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeLibrary,
				PID:       1234,
				CommBytes: [16]uint8{'t', 'e', 's', 't'},
			},
			Inode:     12345,
			MntNSID:   4026532221, // Same namespace as library manager
			PathBytes: makePathBytes("/usr/lib/libssl.so.1.1"),
		}

		lm.ProcessLibraryEvent(event)

		// Check if library was marked as hooked
		hooked, _ := lm.Stats()
		if hooked != 1 {
			t.Errorf("Expected 1 hooked library, got %d", hooked)
		}

		// Check if attach was called
		if len(tl.attachCalls) != 1 {
			t.Errorf("Expected 1 attach call, got %d", len(tl.attachCalls))
		}
		if tl.attachCalls[0] != "/usr/lib/libssl.so.1.1" {
			t.Errorf("Expected attach call with path '/usr/lib/libssl.so.1.1', got '%s'", tl.attachCalls[0])
		}
	})

	// Test failed hook
	t.Run("failed hook", func(t *testing.T) {
		// Set up failure for this path
		tl.attachResults["/usr/lib/libssl.so.3"] = errors.New("probe failed")

		event := &event.LibraryEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeLibrary,
				PID:       1234,
				CommBytes: [16]uint8{'t', 'e', 's', 't'},
			},
			Inode:     67890,
			MntNSID:   4026532221, // Same namespace as library manager
			PathBytes: makePathBytes("/usr/lib/libssl.so.3"),
		}

		lm.ProcessLibraryEvent(event)

		// Check if library was marked as failed
		_, failed := lm.Stats()
		if failed != 1 {
			t.Errorf("Expected 1 failed library, got %d", failed)
		}
	})

	// Test duplicate hook - same inode
	t.Run("duplicate hook", func(t *testing.T) {
		initialCalls := len(tl.attachCalls)

		event := &event.LibraryEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeLibrary,
				PID:       5678,
				CommBytes: [16]uint8{'t', 'e', 's', 't', '2'},
			},
			Inode:     12345,      // Same inode as first test
			MntNSID:   4026532221, // Same namespace as library manager
			PathBytes: makePathBytes("/usr/lib/libssl.so.1.1"),
		}

		lm.ProcessLibraryEvent(event)

		// Check that no additional attach was called
		if len(tl.attachCalls) != initialCalls {
			t.Error("Expected no additional attach calls for duplicate inode")
		}
	})

	// Test previously failed - same inode (will NOT retry for non-retryable errors)
	t.Run("previously failed with non-retryable error", func(t *testing.T) {
		initialCalls := len(tl.attachCalls)

		event := &event.LibraryEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeLibrary,
				PID:       9999,
				CommBytes: [16]uint8{'t', 'e', 's', 't', '3'},
			},
			Inode:     67890,      // Same inode as failed test (with "probe failed" error)
			MntNSID:   4026532221, // Same namespace as library manager
			PathBytes: makePathBytes("/usr/lib/libssl.so.3"),
		}

		// Since "probe failed" is not retryable, this should not retry
		lm.ProcessLibraryEvent(event)

		// Check that attach was NOT called again (no retry for non-retryable error)
		if len(tl.attachCalls) != initialCalls {
			t.Errorf("Expected no additional attach calls for non-retryable error, got %d", len(tl.attachCalls)-initialCalls)
		}
	})

	// Test cross-namespace library (should not attempt to switch since we can't easily test namespace switching)
	t.Run("cross namespace hook", func(t *testing.T) {
		initialCalls := len(tl.attachCalls)

		event := &event.LibraryEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeLibrary,
				PID:       1111,
				CommBytes: [16]uint8{'c', 'r', 'o', 's', 's'},
			},
			Inode:     99999,
			MntNSID:   1234, // Different namespace from library manager
			PathBytes: makePathBytes("/usr/lib/libssl.so.cross"),
		}

		// This will fail because we can't actually switch namespaces in a test
		lm.ProcessLibraryEvent(event)

		// Should not have made additional attach calls (because namespace switching failed)
		if len(tl.attachCalls) != initialCalls {
			t.Error("Expected no additional attach calls for failed cross-namespace switch")
		}
	})

	// Verify final stats
	hooked, failed := lm.Stats()
	if hooked != 1 {
		t.Errorf("Expected 1 hooked library in final stats, got %d", hooked)
	}
	if failed != 2 {
		t.Errorf("Expected 2 failed libraries in final stats, got %d", failed)
	}
}

func TestLibraryManager_GetHookedLibraries(t *testing.T) {
	lm, err := NewLibraryManager(tu.NewMockBus(), nil, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Add some test data directly
	lm.hookedLibs[12345] = "/lib/libssl.so"
	lm.hookedLibs[67890] = "/lib/libcrypto.so"

	hooked := lm.HookedLibraries()

	if len(hooked) != 2 {
		t.Errorf("Expected 2 hooked libraries, got %d", len(hooked))
	}

	if hooked[12345] != "/lib/libssl.so" {
		t.Errorf("Expected inode 12345 to map to '/lib/libssl.so', got '%s'", hooked[12345])
	}

	if hooked[67890] != "/lib/libcrypto.so" {
		t.Errorf("Expected inode 67890 to map to '/lib/libcrypto.so', got '%s'", hooked[67890])
	}
}

func TestLibraryManager_GetFailedLibraries(t *testing.T) {
	lm, err := NewLibraryManager(tu.NewMockBus(), nil, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Add some test data directly
	err1 := errors.New("permission denied")
	err2 := errors.New("file not found")
	lm.failedLibs[11111] = err1
	lm.failedLibs[22222] = err2

	failed := lm.FailedLibraries()

	if len(failed) != 2 {
		t.Errorf("Expected 2 failed libraries, got %d", len(failed))
	}

	if failed[11111] != err1 {
		t.Errorf("Expected inode 11111 to have error 'permission denied', got '%v'", failed[11111])
	}

	if failed[22222] != err2 {
		t.Errorf("Expected inode 22222 to have error 'file not found', got '%v'", failed[22222])
	}
}

func TestLibraryManager_Reset(t *testing.T) {
	lm, err := NewLibraryManager(tu.NewMockBus(), nil, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Add some test data
	lm.hookedLibs[12345] = "/lib/libssl.so"
	lm.failedLibs[67890] = errors.New("test error")

	// Verify we have entries
	hooked, failed := lm.Stats()
	if hooked != 1 || failed != 1 {
		t.Errorf("Expected 1 hooked and 1 failed before reset, got %d hooked and %d failed", hooked, failed)
	}

	// Reset
	lm.Clean()

	// Verify everything is cleared
	hooked, failed = lm.Stats()
	if hooked != 0 || failed != 0 {
		t.Errorf("Expected 0 hooked and 0 failed after reset, got %d hooked and %d failed", hooked, failed)
	}

	if len(lm.HookedLibraries()) != 0 {
		t.Error("Expected empty hooked libraries map after reset")
	}

	if len(lm.FailedLibraries()) != 0 {
		t.Error("Expected empty failed libraries map after reset")
	}
}

func TestLibraryManager_Close(t *testing.T) {
	lm, err := NewLibraryManager(tu.NewMockBus(), nil, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Close should work without error
	err = lm.Close()
	if err != nil {
		t.Errorf("Expected no error from Close(), got %v", err)
	}

	// Multiple closes should not cause issues
	err = lm.Close()
	if err != nil {
		t.Errorf("Expected no error from second Close(), got %v", err)
	}
}

// Test retry behavior with non-retryable errors
func TestLibraryManager_NonRetryableError(t *testing.T) {
	tl := newTestLoader()
	lm, err := NewLibraryManager(tu.NewMockBus(), tl, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Set up failure for this path with a non-retryable error
	tl.attachResults["/usr/lib/libssl.so.retry"] = errors.New("probe failed")

	// First attempt should fail
	event := &event.LibraryEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeLibrary,
			PID:       1234,
			CommBytes: [16]uint8{'r', 'e', 't', 'r', 'y'},
		},
		Inode:     99999,
		MntNSID:   4026532221,
		PathBytes: makePathBytes("/usr/lib/libssl.so.retry"),
	}

	lm.ProcessLibraryEvent(event)

	// Verify it was marked as failed
	_, failed := lm.Stats()
	if failed != 1 {
		t.Errorf("Expected 1 failed library, got %d", failed)
	}

	// Second attempt should be skipped because error is not retryable
	initialCalls := len(tl.attachCalls)
	lm.ProcessLibraryEvent(event)

	// Check that no additional attach was called
	if len(tl.attachCalls) != initialCalls {
		t.Error("Expected no additional attach calls for non-retryable error")
	}
}

// Test retry behavior with retryable errors (like "no such file or directory")
func TestLibraryManager_RetryableError(t *testing.T) {
	tl := newTestLoader()
	lm, err := NewLibraryManager(tu.NewMockBus(), tl, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Set up failure for this path with a retryable error
	tl.attachResults["/usr/lib/libssl.so.retryable"] = errors.New("failed to open executable: no such file or directory")

	// First attempt should fail
	event := &event.LibraryEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeLibrary,
			PID:       1234,
			CommBytes: [16]uint8{'r', 'e', 't', 'r', 'y'},
		},
		Inode:     88888,
		MntNSID:   4026532221,
		PathBytes: makePathBytes("/usr/lib/libssl.so.retryable"),
	}

	lm.ProcessLibraryEvent(event)

	// Verify it was marked as failed
	_, failed := lm.Stats()
	if failed != 1 {
		t.Errorf("Expected 1 failed library, got %d", failed)
	}

	// Second attempt should retry because error is retryable
	initialCalls := len(tl.attachCalls)
	lm.ProcessLibraryEvent(event)

	// Check that attach was called again (retry behavior)
	if len(tl.attachCalls) != initialCalls+1 {
		t.Errorf("Expected 1 additional attach call for retryable error, got %d", len(tl.attachCalls)-initialCalls)
	}
}

// Test error state removal when load succeeds
func TestLibraryManager_ErrorStateRemoval(t *testing.T) {
	tl := newTestLoader()
	lm, err := NewLibraryManager(tu.NewMockBus(), tl, 4026532221)
	if err != nil {
		t.Fatalf("Failed to create LibraryManager: %v", err)
	}
	defer lm.Close()

	// Set up initial failure with a retryable error
	tl.attachResults["/usr/lib/libssl.so.recover"] = errors.New("failed to open: no such file or directory")

	event := &event.LibraryEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeLibrary,
			PID:       1234,
			CommBytes: [16]uint8{'r', 'e', 'c', 'o', 'v', 'e', 'r'},
		},
		Inode:     88888,
		MntNSID:   4026532221,
		PathBytes: makePathBytes("/usr/lib/libssl.so.recover"),
	}

	// First attempt should fail
	lm.ProcessLibraryEvent(event)

	// Verify it was marked as failed
	_, failed := lm.Stats()
	if failed != 1 {
		t.Errorf("Expected 1 failed library, got %d", failed)
	}

	// Remove the failure condition
	delete(tl.attachResults, "/usr/lib/libssl.so.recover")

	// Second attempt should succeed
	lm.ProcessLibraryEvent(event)
	if err != nil {
		t.Errorf("Expected success on retry, got %v", err)
	}

	// Verify it was moved from failed to hooked
	hooked, failed := lm.Stats()
	if hooked != 1 {
		t.Errorf("Expected 1 hooked library, got %d", hooked)
	}
	if failed != 0 {
		t.Errorf("Expected 0 failed libraries after success, got %d", failed)
	}

	// Third attempt should be skipped (already hooked)
	initialCalls := len(tl.attachCalls)
	lm.ProcessLibraryEvent(event)

	// Check that no additional attach was called
	if len(tl.attachCalls) != initialCalls {
		t.Error("Expected no additional attach calls for already hooked library")
	}
}

// Helper function to create PathBytes array from string
func makePathBytes(path string) [512]uint8 {
	var result [512]uint8
	copy(result[:], []byte(path))
	return result
}
