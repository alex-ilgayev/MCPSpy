package ebpf

import (
	"errors"
	"testing"
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
	lm := NewLibraryManager(tl)

	// Test successful hook
	t.Run("successful hook", func(t *testing.T) {
		event := &LibraryEvent{
			EventHeader: EventHeader{
				EventType: EventTypeLibrary,
				PID:       1234,
				CommBytes: [16]uint8{'t', 'e', 's', 't'},
			},
			Inode:     12345,
			PathBytes: makePathBytes("/usr/lib/libssl.so.1.1"),
		}

		err := lm.ProcessLibraryEvent(event)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

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

		event := &LibraryEvent{
			EventHeader: EventHeader{
				EventType: EventTypeLibrary,
				PID:       1234,
				CommBytes: [16]uint8{'t', 'e', 's', 't'},
			},
			Inode:     67890,
			PathBytes: makePathBytes("/usr/lib/libssl.so.3"),
		}

		err := lm.ProcessLibraryEvent(event)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		// Check if library was marked as failed
		_, failed := lm.Stats()
		if failed != 1 {
			t.Errorf("Expected 1 failed library, got %d", failed)
		}
	})

	// Test duplicate hook - same inode
	t.Run("duplicate hook", func(t *testing.T) {
		initialCalls := len(tl.attachCalls)

		event := &LibraryEvent{
			EventHeader: EventHeader{
				EventType: EventTypeLibrary,
				PID:       5678,
				CommBytes: [16]uint8{'t', 'e', 's', 't', '2'},
			},
			Inode:     12345, // Same inode as first test
			PathBytes: makePathBytes("/usr/lib/libssl.so.1.1"),
		}

		err := lm.ProcessLibraryEvent(event)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		// Check that no additional attach was called
		if len(tl.attachCalls) != initialCalls {
			t.Error("Expected no additional attach calls for duplicate inode")
		}
	})

	// Test previously failed - same inode
	t.Run("previously failed", func(t *testing.T) {
		initialCalls := len(tl.attachCalls)

		event := &LibraryEvent{
			EventHeader: EventHeader{
				EventType: EventTypeLibrary,
				PID:       9999,
				CommBytes: [16]uint8{'t', 'e', 's', 't', '3'},
			},
			Inode:     67890, // Same inode as failed test
			PathBytes: makePathBytes("/usr/lib/libssl.so.3"),
		}

		err := lm.ProcessLibraryEvent(event)
		if err != nil {
			t.Errorf("Expected no error for cached failure, got %v", err)
		}

		// Check that no additional attach was called
		if len(tl.attachCalls) != initialCalls {
			t.Error("Expected no additional attach calls for cached failure")
		}
	})

	// Verify final stats
	hooked, failed := lm.Stats()
	if hooked != 1 {
		t.Errorf("Expected 1 hooked library in final stats, got %d", hooked)
	}
	if failed != 1 {
		t.Errorf("Expected 1 failed library in final stats, got %d", failed)
	}
}

func TestLibraryManager_GetHookedLibraries(t *testing.T) {
	lm := NewLibraryManager(nil)

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
	lm := NewLibraryManager(nil)

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
	lm := NewLibraryManager(nil)

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

// Helper function to create PathBytes array from string
func makePathBytes(path string) [512]uint8 {
	var result [512]uint8
	copy(result[:], []byte(path))
	return result
}
