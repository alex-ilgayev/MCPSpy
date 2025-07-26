//go:build embed_ecapture
// +build embed_ecapture

package embed

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// Embed the ecapture binary
//
//go:embed ecapture
var ecaptureBinary []byte

// HasEmbeddedEcapture returns true if ecapture is embedded
func HasEmbeddedEcapture() bool {
	return len(ecaptureBinary) > 0
}

// ExtractEcapture extracts the embedded ecapture binary to a temporary location
// and returns the path to the executable
func ExtractEcapture() (string, error) {
	if len(ecaptureBinary) == 0 {
		return "", fmt.Errorf("ecapture binary not embedded")
	}

	// Create a temporary directory for the binary
	tmpDir, err := os.MkdirTemp("", "mcpspy-ecapture-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Path for the extracted binary
	ecapturePath := filepath.Join(tmpDir, "ecapture")

	// Write the binary to disk
	if err := os.WriteFile(ecapturePath, ecaptureBinary, 0755); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to write ecapture binary: %w", err)
	}

	logrus.Debugf("Extracted embedded ecapture to: %s", ecapturePath)
	return ecapturePath, nil
}

// CleanupEcapture removes the temporary directory containing the ecapture binary
func CleanupEcapture(ecapturePath string) error {
	if ecapturePath == "" {
		return nil
	}

	// Get the directory containing the binary
	dir := filepath.Dir(ecapturePath)

	// Remove the entire temporary directory
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to cleanup ecapture: %w", err)
	}

	logrus.Debug("Cleaned up ecapture temporary files")
	return nil
}
