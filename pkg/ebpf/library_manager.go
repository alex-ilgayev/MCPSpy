package ebpf

import (
	"fmt"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/sirupsen/logrus"
)

// SSLProbeAttacher is an interface for attaching SSL probes to libraries
type SSLProbeAttacher interface {
	AttachSSLProbes(libraryPath string) error
}

// LibraryManager manages uprobe hooks for dynamically loaded libraries.
// It prevents duplicate hooks and caches failed attempts.
type LibraryManager struct {
	attacher   SSLProbeAttacher
	hookedLibs map[uint64]string // inode -> path (successfully hooked)
	failedLibs map[uint64]error  // inode -> error (failed to hook)
	mu         sync.Mutex
}

// NewLibraryManager creates a new library manager
func NewLibraryManager(attacher SSLProbeAttacher) *LibraryManager {
	return &LibraryManager{
		attacher:   attacher,
		hookedLibs: make(map[uint64]string),
		failedLibs: make(map[uint64]error),
	}
}

// ProcessLibraryEvent processes a library event and attempts to attach SSL probes if needed
func (lm *LibraryManager) ProcessLibraryEvent(event *event.LibraryEvent) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	inode := event.Inode
	path := event.Path()

	// Check if already hooked
	if hookedPath, ok := lm.hookedLibs[inode]; ok {
		logrus.WithFields(logrus.Fields{
			"inode":       inode,
			"path":        path,
			"hooked_path": hookedPath,
		}).Trace("Library already hooked")
		return nil
	}

	// Check if previously failed
	if err, ok := lm.failedLibs[inode]; ok {
		logrus.WithFields(logrus.Fields{
			"inode": inode,
			"path":  path,
			"error": err,
		}).Trace("Library previously failed to hook, skipping")
		return nil
	}

	if err := lm.attacher.AttachSSLProbes(path); err != nil {
		// Cache the failure
		lm.failedLibs[inode] = err
		return fmt.Errorf("failed to attach SSL probes to %s (inode %d): %w", path, inode, err)
	}

	lm.hookedLibs[inode] = path
	logrus.WithFields(logrus.Fields{
		"inode": inode,
		"path":  path,
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
