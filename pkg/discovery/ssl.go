package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// SSLTarget represents a discovered SSL library or binary
type SSLTarget struct {
	Path       string
	Type       SSLType
	PID        uint32 // 0 if not from a running process
	BinaryName string
}

// SSLType indicates how SSL is linked
type SSLType int

const (
	SSLTypeDynamic SSLType = iota // Dynamically linked libssl.so
	SSLTypeStatic                 // Statically linked (e.g., node)
)

// Common binary names that statically link OpenSSL
var staticSSLBinaries = []string{
	"node",
	"deno",
	"python3",
	"python",
	"curl",
	"nginx",
	"envoy",
}

// SSLDiscovery handles finding SSL libraries
type SSLDiscovery struct {
	targets []SSLTarget
}

// NewSSLDiscovery creates a new SSL discovery instance
func NewSSLDiscovery() *SSLDiscovery {
	return &SSLDiscovery{
		targets: make([]SSLTarget, 0),
	}
}

// Discover finds all SSL libraries and binaries
func (d *SSLDiscovery) Discover() ([]SSLTarget, error) {
	d.targets = make([]SSLTarget, 0)

	fmt.Println("Discovering dynamic SSL libraries")
	// 1. Search for dynamic libssl libraries
	if err := d.discoverDynamicLibraries(); err != nil {
		logrus.WithError(err).Warn("Failed to discover dynamic SSL libraries")
	}

	fmt.Println("Discovering static SSL binaries")
	// 2. Search for statically linked binaries
	if err := d.discoverStaticBinaries(); err != nil {
		logrus.WithError(err).Warn("Failed to discover static SSL binaries")
	}

	fmt.Println("Discovering SSL from /proc")
	// 3. Search in running processes
	if err := d.discoverFromProc(); err != nil {
		logrus.WithError(err).Warn("Failed to discover SSL from /proc")
	}

	fmt.Println("Deduplicating SSL targets")
	return d.deduplicate(), nil
}

// discoverDynamicLibraries searches common paths for libssl.so
func (d *SSLDiscovery) discoverDynamicLibraries() error {
	fmt.Println("Discovering dynamic SSL libraries")
	searchPaths := []string{
		"/usr/lib",
		"/usr/lib64",
		"/usr/local/lib",
		"/usr/local/lib64",
		"/lib",
		"/lib64",
		"/opt/*/lib",
		"/opt/*/lib64",
	}

	for _, searchPath := range searchPaths {
		fmt.Println("Searching for dynamic SSL libraries in", searchPath)
		// Handle glob patterns
		matches, _ := filepath.Glob(searchPath)
		if len(matches) == 0 {
			matches = []string{searchPath}
		}

		for _, path := range matches {
			if err := d.searchLibSSLInDir(path); err != nil {
				continue
			}
		}
	}
	fmt.Println("Found", len(d.targets), "dynamic SSL libraries")

	return nil
}

// searchLibSSLInDir searches for libssl in a specific directory
func (d *SSLDiscovery) searchLibSSLInDir(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}

		// Look for libssl.so variants
		if strings.Contains(info.Name(), "libssl.so") && !info.IsDir() {
			d.targets = append(d.targets, SSLTarget{
				Path:       path,
				Type:       SSLTypeDynamic,
				BinaryName: "libssl",
			})
			logrus.Debugf("Found dynamic SSL library: %s", path)
		}

		return nil
	})
}

// discoverStaticBinaries searches for known binaries with static SSL
func (d *SSLDiscovery) discoverStaticBinaries() error {
	fmt.Println("Discovering static SSL binaries")
	// Search in PATH
	pathEnv := os.Getenv("PATH")
	pathDirs := strings.Split(pathEnv, ":")

	// Add common binary locations
	pathDirs = append(pathDirs,
		"/usr/bin",
		"/usr/local/bin",
		"/opt/*/bin",
		"/usr/sbin",
		"/usr/local/sbin",
	)

	for _, dir := range pathDirs {
		// Handle glob patterns
		matches, _ := filepath.Glob(dir)
		if len(matches) == 0 {
			matches = []string{dir}
		}

		for _, path := range matches {
			d.searchStaticBinariesInDir(path)
		}
	}

	return nil
}

// searchStaticBinariesInDir searches for static SSL binaries in a directory
func (d *SSLDiscovery) searchStaticBinariesInDir(dir string) {
	for _, binaryName := range staticSSLBinaries {
		fullPath := filepath.Join(dir, binaryName)
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			d.targets = append(d.targets, SSLTarget{
				Path:       fullPath,
				Type:       SSLTypeStatic,
				BinaryName: binaryName,
			})
			logrus.Debugf("Found static SSL binary: %s", fullPath)
		}
	}
}

// discoverFromProc searches /proc for processes using SSL
func (d *SSLDiscovery) discoverFromProc() error {
	procDir := "/proc"
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return fmt.Errorf("failed to read /proc: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if it's a PID directory
		pid := uint32(0)
		if _, err := fmt.Sscanf(entry.Name(), "%d", &pid); err != nil {
			continue
		}

		// Check memory maps
		d.checkProcessMaps(pid)
	}

	return nil
}

// checkProcessMaps checks /proc/[pid]/maps for SSL libraries
func (d *SSLDiscovery) checkProcessMaps(pid uint32) {
	mapsPath := fmt.Sprintf("/proc/%d/maps", pid)
	data, err := os.ReadFile(mapsPath)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, "libssl.so") {
			// Extract the library path
			fields := strings.Fields(line)
			if len(fields) >= 6 {
				libPath := fields[5]
				d.targets = append(d.targets, SSLTarget{
					Path:       libPath,
					Type:       SSLTypeDynamic,
					PID:        pid,
					BinaryName: "libssl",
				})
				logrus.Debugf("Found SSL library in process %d: %s", pid, libPath)
			}
		}
	}

	// Also check if the process is one of our static binaries
	exePath := fmt.Sprintf("/proc/%d/exe", pid)
	if exe, err := os.Readlink(exePath); err == nil {
		baseName := filepath.Base(exe)
		for _, staticBinary := range staticSSLBinaries {
			if baseName == staticBinary {
				d.targets = append(d.targets, SSLTarget{
					Path:       exe,
					Type:       SSLTypeStatic,
					PID:        pid,
					BinaryName: baseName,
				})
				logrus.Debugf("Found static SSL binary process %d: %s", pid, exe)
				break
			}
		}
	}
}

// deduplicate removes duplicate targets
func (d *SSLDiscovery) deduplicate() []SSLTarget {
	seen := make(map[string]bool)
	result := make([]SSLTarget, 0)

	for _, target := range d.targets {
		key := fmt.Sprintf("%s:%d", target.Path, target.Type)
		if !seen[key] {
			seen[key] = true
			result = append(result, target)
		}
	}

	return result
}
