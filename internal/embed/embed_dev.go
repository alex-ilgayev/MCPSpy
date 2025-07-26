//go:build !embed_ecapture
// +build !embed_ecapture

package embed

import (
	"fmt"
)

// HasEmbeddedEcapture returns false in dev builds
func HasEmbeddedEcapture() bool {
	return false
}

// ExtractEcapture returns an error in dev builds
func ExtractEcapture() (string, error) {
	return "", fmt.Errorf("ecapture binary not embedded in dev build")
}

// CleanupEcapture does nothing in dev builds
func CleanupEcapture(ecapturePath string) error {
	return nil
}
