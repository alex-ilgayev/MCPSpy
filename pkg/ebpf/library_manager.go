package ebpf

import (
	"strings"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
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
	attacher   SSLProbeAttacher
	mountNS    uint32            // mount namespace ID
	hookedLibs map[uint64]string // inode -> path (successfully hooked)
	failedLibs map[uint64]error  // inode -> error (failed to hook)
	eventBus   bus.EventBus
	mu         sync.Mutex
}

// NewLibraryManager creates a new library manager
func NewLibraryManager(eventBus bus.EventBus, attacher SSLProbeAttacher, mountNS uint32) (*LibraryManager, error) {
	lm := &LibraryManager{
		attacher:   attacher,
		mountNS:    mountNS,
		hookedLibs: make(map[uint64]string),
		failedLibs: make(map[uint64]error),
		eventBus:   eventBus,
	}

	// Subscribe to library events
	if err := eventBus.Subscribe(event.EventTypeLibrary, lm.ProcessLibraryEvent); err != nil {
		return nil, err
	}

	return lm, nil
}

// retryableErrorPatterns contains error patterns that should trigger a retry
var retryableErrorPatterns = []string{
	"no such file or directory",
}

// isRetryableError checks if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := strings.ToLower(err.Error())
	for _, pattern := range retryableErrorPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}
	return false
}

// ProcessLibraryEvent processes a library event and attempts to attach SSL probes if needed
func (lm *LibraryManager) ProcessLibraryEvent(e event.Event) {
	// We only handle LibraryEvent types
	libEvent, ok := e.(*event.LibraryEvent)
	if !ok {
		return
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	inode := libEvent.Inode
	path := libEvent.Path()
	targetMountNS := libEvent.MountNamespaceID()

	// Check if already hooked
	if hookedPath, ok := lm.hookedLibs[inode]; ok {
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"hooked_path":   hookedPath,
			"target_mnt_ns": targetMountNS,
		}).Trace("Library already hooked")
		return
	}

	// Check if previously failed and error is not retryable
	if err, ok := lm.failedLibs[inode]; ok && !isRetryableError(err) {
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"error":         err,
			"target_mnt_ns": targetMountNS,
		}).Trace("Library previously failed to hook with non-retryable error, skipping")
		return
	}

	var modifiedPath string
	var err error

	// Check if we need to fetch path in a different mount namespace
	if targetMountNS != lm.mountNS {
		// Different namespace - need to modify path
		modifiedPath, err = namespace.GetPathInMountNamespace(path, targetMountNS)
		if err != nil {
			lm.failedLibs[inode] = err
			logrus.WithFields(logrus.Fields{
				"inode":         inode,
				"path":          path,
				"target_mnt_ns": targetMountNS,
			}).Warn("Failed to get path in mount namespace")
			return
		}
	} else {
		// Same namespace - no need path modification
		modifiedPath = path
	}

	if err := lm.attacher.AttachSSLProbes(modifiedPath); err != nil {
		lm.failedLibs[inode] = err
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"target_mnt_ns": targetMountNS,
		}).Warn("Failed to attach SSL probes")
		return
	}

	// Successfully attached - remove from failed libs if it was there
	delete(lm.failedLibs, inode)
	lm.hookedLibs[inode] = path
	logrus.WithFields(logrus.Fields{
		"inode":         inode,
		"path":          path,
		"target_mnt_ns": targetMountNS,
	}).Debug("Successfully attached SSL probes to library")
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
