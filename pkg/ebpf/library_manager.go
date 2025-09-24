package ebpf

import (
	"fmt"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/namespace"
	"github.com/sirupsen/logrus"
)

// SSLProbeAttacher is an interface for attaching SSL probes to libraries
type SSLProbeAttacher interface {
	AttachSSLProbes(libraryPath string) error
}

// LibraryManager manages uprobe hooks for dynamically loaded libraries.
// It prevents duplicate hooks and caches failed attempts.
type LibraryManager struct {
	attacher     SSLProbeAttacher
	mountNS      uint32            // mount namespace ID
	retryOnError bool              // whether to retry failed libraries
	hookedLibs   map[uint64]string // inode -> path (successfully hooked)
	failedLibs   map[uint64]error  // inode -> error (failed to hook)
	mu           sync.Mutex
}

// NewLibraryManager creates a new library manager
func NewLibraryManager(attacher SSLProbeAttacher, mountNS uint32) *LibraryManager {
	return NewLibraryManagerWithRetry(attacher, mountNS, true)
}

// NewLibraryManagerWithRetry creates a new library manager with configurable retry behavior
func NewLibraryManagerWithRetry(attacher SSLProbeAttacher, mountNS uint32, retryOnError bool) *LibraryManager {
	return &LibraryManager{
		attacher:     attacher,
		mountNS:      mountNS,
		retryOnError: retryOnError,
		hookedLibs:   make(map[uint64]string),
		failedLibs:   make(map[uint64]error),
	}
}

// ProcessLibraryEvent processes a library event and attempts to attach SSL probes if needed
func (lm *LibraryManager) ProcessLibraryEvent(event *event.LibraryEvent) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	inode := event.Inode
	path := event.Path()
	targetMountNS := event.MountNamespaceID()

	// Check if already hooked
	if hookedPath, ok := lm.hookedLibs[inode]; ok {
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"hooked_path":   hookedPath,
			"target_mnt_ns": targetMountNS,
		}).Trace("Library already hooked")
		return nil
	}

	// Check if previously failed and retryOnError is disabled
	if err, ok := lm.failedLibs[inode]; ok && !lm.retryOnError {
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"error":         err,
			"target_mnt_ns": targetMountNS,
		}).Trace("Library previously failed to hook, skipping")
		return nil
	}

	var modifiedPath string
	var err error

	// Check if we need to fetch path in a different mount namespace
	if targetMountNS != lm.mountNS {
		// Different namespace - need to modify path
		modifiedPath, err = namespace.GetPathInMountNamespace(path, targetMountNS)
		if err != nil {
			return fmt.Errorf("failed to get path in mount namespace for %s (inode %d) in mount namespace %d: %w",
				path, inode, targetMountNS, err)
		}
	} else {
		// Same namespace - no need path modification
		modifiedPath = path
	}

	if err := lm.attacher.AttachSSLProbes(modifiedPath); err != nil {
		lm.failedLibs[inode] = err
		return fmt.Errorf("failed to attach SSL probes to %s (inode %d): %w", modifiedPath, inode, err)
	}

	// Successfully attached - remove from failed libs if it was there
	delete(lm.failedLibs, inode)
	lm.hookedLibs[inode] = path
	logrus.WithFields(logrus.Fields{
		"inode":         inode,
		"path":          path,
		"target_mnt_ns": targetMountNS,
	}).Debug("Successfully attached SSL probes to library")

	return nil
}

// Stats returns statistics about hooked and failed libraries
func (lm *LibraryManager) Stats() (hooked int, failed int) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return len(lm.hookedLibs), len(lm.failedLibs)
}

// HookedLibraries returns a copy of the hooked libraries map
func (lm *LibraryManager) HookedLibraries() map[uint64]string {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	result := make(map[uint64]string, len(lm.hookedLibs))
	for k, v := range lm.hookedLibs {
		result[k] = v
	}
	return result
}

// FailedLibraries returns a copy of the failed libraries map
func (lm *LibraryManager) FailedLibraries() map[uint64]error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	result := make(map[uint64]error, len(lm.failedLibs))
	for k, v := range lm.failedLibs {
		result[k] = v
	}
	return result
}

// Clean clears all tracked libraries (useful for testing)
func (lm *LibraryManager) Clean() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lm.hookedLibs = make(map[uint64]string)
	lm.failedLibs = make(map[uint64]error)
}

// Close closes the library manager and cleans up resources
func (lm *LibraryManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// TODO: We need to remove attachments here.
	return nil
}
