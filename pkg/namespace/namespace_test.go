package namespace

import (
	"os"
	"testing"
)

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

func TestGetPathInMountNamespace(t *testing.T) {
	t.Run("CurrentMountNamespace", func(t *testing.T) {
		// Get the current process's mount namespace
		currentNsID, err := GetCurrentMountNamespace()
		if err != nil {
			t.Fatalf("GetCurrentMountNamespace() failed: %v", err)
		}

		// Test with a valid path in the current mount namespace
		path := "/tmp"
		rootPath, err := GetPathInMountNamespace(path, currentNsID)
		if err != nil {
			t.Fatalf("GetPathInMountNamespace(%q, %d) failed: %v", path, currentNsID, err)
		}

		// The result should be in the format /proc/<pid>/root/tmp
		t.Logf("GetPathInMountNamespace(%q, %d) = %q", path, currentNsID, rootPath)
	})

	t.Run("InvalidPath", func(t *testing.T) {
		currentNsID, err := GetCurrentMountNamespace()
		if err != nil {
			t.Fatalf("GetCurrentMountNamespace() failed: %v", err)
		}

		path := "tmp"
		_, err = GetPathInMountNamespace(path, currentNsID)
		if err == nil {
			t.Error("Expected error for invalid path")
		}
	})

	t.Run("NonexistentNamespace", func(t *testing.T) {
		// Use a very unlikely namespace ID
		nonexistentNsID := uint32(999999999)
		path := "/tmp"
		_, err := GetPathInMountNamespace(path, nonexistentNsID)
		if err == nil {
			t.Error("Expected error for nonexistent namespace")
		}
	})
}
