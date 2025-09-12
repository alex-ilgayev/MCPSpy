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
	attacher   SSLProbeAttacher
	mountNS    uint64                            // mount namespace ID
	nsSwitcher *namespace.MountNamespaceSwitcher // for switching mount namespaces
	hookedLibs map[uint64]string                 // inode -> path (successfully hooked)
	failedLibs map[uint64]error                  // inode -> error (failed to hook)
	mu         sync.Mutex
}

// NewLibraryManager creates a new library manager
func NewLibraryManager(attacher SSLProbeAttacher, mountNS uint64) (*LibraryManager, error) {
	nsSwitcher, err := namespace.NewMountNamespaceSwitcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create mount namespace switcher: %w", err)
	}

	return &LibraryManager{
		attacher:   attacher,
		mountNS:    mountNS,
		nsSwitcher: nsSwitcher,
		hookedLibs: make(map[uint64]string),
		failedLibs: make(map[uint64]error),
	}, nil
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

	// Check if previously failed
	if err, ok := lm.failedLibs[inode]; ok {
		logrus.WithFields(logrus.Fields{
			"inode":         inode,
			"path":          path,
			"error":         err,
			"target_mnt_ns": targetMountNS,
		}).Trace("Library previously failed to hook, skipping")
		return nil
	}

	// Check if we need to switch mount namespaces
	if uint64(targetMountNS) != lm.mountNS {
		if err := lm.attachSSLProbesInNamespace(path, targetMountNS, inode); err != nil {
			lm.failedLibs[inode] = err
			return fmt.Errorf("failed to attach SSL probes to %s (inode %d) in mount namespace %d: %w",
				path, inode, targetMountNS, err)
		}
	} else {
		// Same namespace - use direct attachment
		if err := lm.attacher.AttachSSLProbes(path); err != nil {
			lm.failedLibs[inode] = err
			return fmt.Errorf("failed to attach SSL probes to %s (inode %d): %w", path, inode, err)
		}
	}

	lm.hookedLibs[inode] = path
	logrus.WithFields(logrus.Fields{
		"inode":          inode,
		"path":           path,
		"target_mnt_ns":  targetMountNS,
		"current_mnt_ns": lm.mountNS,
	}).Debug("Successfully attached SSL probes to library")

	return nil
}

// attachSSLProbesInNamespace attaches SSL probes to a library in a different mount namespace
func (lm *LibraryManager) attachSSLProbesInNamespace(path string, targetMountNS uint32, inode uint64) error {
	logrus.WithFields(logrus.Fields{
		"path":           path,
		"target_mnt_ns":  targetMountNS,
		"current_mnt_ns": lm.mountNS,
		"inode":          inode,
	}).Debug("Switching mount namespace to attach SSL probes")

	// Switch to target namespace
	if err := lm.nsSwitcher.SwitchTo(targetMountNS); err != nil {
		return fmt.Errorf("failed to switch to mount namespace %d: %w", targetMountNS, err)
	}

	// Ensure we restore the original namespace even if AttachSSLProbes fails
	var attachErr error
	defer func() {
		if restoreErr := lm.nsSwitcher.Restore(); restoreErr != nil {
			logrus.WithFields(logrus.Fields{
				"path":          path,
				"target_mnt_ns": targetMountNS,
				"restore_error": restoreErr,
				"attach_error":  attachErr,
			}).Error("Failed to restore mount namespace after SSL probe attachment")
		}
	}()

	// Attach SSL probes in the target namespace
	attachErr = lm.attacher.AttachSSLProbes(path)
	return attachErr
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

	lastErr := error(nil)

	if lm.nsSwitcher != nil {
		lastErr = lm.nsSwitcher.Close()
		lm.nsSwitcher = nil
	}

	return lastErr
}
