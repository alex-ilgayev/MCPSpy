package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
	"github.com/alex-ilgayev/mcpspy/pkg/fs"
	"github.com/alex-ilgayev/mcpspy/pkg/http"
	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
	"github.com/alex-ilgayev/mcpspy/pkg/namespace"
	"github.com/alex-ilgayev/mcpspy/pkg/output"
	"github.com/alex-ilgayev/mcpspy/pkg/version"

	mcpspydebug "github.com/alex-ilgayev/mcpspy/pkg/debug"
)

// Command line flags
var (
	showBuffers bool
	verbose     bool
	outputFile  string
	logLevel    string
	tui         bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "mcpspy",
		Short: "Monitor Model Context Protocol communication",
		Long: `MCPSpy is a CLI utility that uses eBPF to monitor MCP (Model Context Protocol)
communication by tracking stdio operations and analyzing JSON-RPC 2.0 messages.`,
		Version:      fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.Commit, version.Date),
		RunE:         run,
		SilenceUsage: true,
	}

	// Add flags
	rootCmd.Flags().BoolVarP(&showBuffers, "buffers", "b", false, "Show raw message buffers (static mode only)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file (JSONL format will be written to file)")
	rootCmd.Flags().StringVarP(&logLevel, "log-level", "l", "info", "Set log level (trace, debug, info, warn, error, fatal, panic)")
	rootCmd.Flags().BoolVar(&tui, "tui", false, "Enable TUI (Terminal UI) mode")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// chownToOriginalUser changes the ownership of a file to the original user
// who invoked sudo. This allows the user to access files created by mcpspy
// without needing sudo privileges.
func chownToOriginalUser(filepath string) error {
	// Get the original user's UID and GID from environment variables
	// These are set by sudo when the program is run with elevated privileges
	sudoUID := os.Getenv("SUDO_UID")
	sudoGID := os.Getenv("SUDO_GID")

	// If not running under sudo, nothing to do
	if sudoUID == "" || sudoGID == "" {
		logrus.Debug("Not running under sudo, skipping chown")
		return nil
	}

	uid, err := strconv.Atoi(sudoUID)
	if err != nil {
		return fmt.Errorf("failed to parse SUDO_UID '%s': %w", sudoUID, err)
	}

	gid, err := strconv.Atoi(sudoGID)
	if err != nil {
		return fmt.Errorf("failed to parse SUDO_GID '%s': %w", sudoGID, err)
	}

	// Change ownership to the original user
	if err := os.Chown(filepath, uid, gid); err != nil {
		return fmt.Errorf("failed to chown '%s' to uid=%d gid=%d: %w", filepath, uid, gid, err)
	}

	logrus.WithFields(logrus.Fields{
		"file": filepath,
		"uid":  uid,
		"gid":  gid,
	}).Debug("Changed file ownership to original user")

	return nil
}

func run(cmd *cobra.Command, args []string) error {
	// Set up logging
	// Handle verbose flag as shortcut for debug level
	if verbose {
		logLevel = "debug"
	}

	// Parse and set log level
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level '%s': %w", logLevel, err)
	}

	// In TUI mode, disable debug logs to prevent display corruption
	// Only show ERROR and above (temporary solution until debug viewer is implemented)
	if tui && level > logrus.ErrorLevel {
		level = logrus.ErrorLevel
	}

	logrus.SetLevel(level)

	// Setup trace pipe to debug eBPF programs if debug or trace level
	if level >= logrus.DebugLevel {
		go mcpspydebug.PrintTracePipe(logrus.StandardLogger())
	}

	// Fetch current mount namespace
	mountNS, err := namespace.GetCurrentMountNamespace()
	if err != nil {
		return fmt.Errorf("failed to get current mount namespace: %w", err)
	}
	logrus.WithField("mount_ns", mountNS).Debug("Current mount namespace")

	// A publish/subscribe event bus for inter-component communication
	eventBus := bus.New()
	defer eventBus.Close()

	// Set up display based on mode
	var tuiDisplay *output.TUIDisplay
	var consoleDisplay *output.ConsoleDisplay

	if tui {
		// TUI mode: use interactive TUI
		tuiDisplay, err = output.NewTUIDisplay(eventBus)
		if err != nil {
			return fmt.Errorf("failed to create TUI display: %w", err)
		}
	} else {
		// Static mode: use console display (default)
		consoleDisplay, err = output.NewConsoleDisplay(os.Stdout, showBuffers, eventBus)
		if err != nil {
			return fmt.Errorf("failed to create console display: %w", err)
		}
		consoleDisplay.PrintHeader()
	}

	// Set up file output if specified
	if outputFile != "" {
		file, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file '%s': %w", outputFile, err)
		}
		// Change ownership to original user (if running under sudo)
		// so the user can access the file without sudo after mcpspy exits
		if err := chownToOriginalUser(outputFile); err != nil {
			logrus.WithError(err).Debug("Failed to change ownership of output file")
		}
		_, err = output.NewJSONLDisplay(file, eventBus)
		if err != nil {
			return fmt.Errorf("failed to create file display: %w", err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				logrus.WithError(err).Error("Failed to close output file")
			}
		}()
	}

	// Create and load eBPF program
	loader, err := ebpf.New(uint32(os.Getpid()), eventBus)
	if err != nil {
		return fmt.Errorf("failed to create eBPF loader: %w", err)
	}
	defer loader.Close()

	// Process library events
	// and creates uprobe hooks for dynamically loaded libraries
	libManager, err := ebpf.NewLibraryManager(eventBus, loader, mountNS)
	if err != nil {
		return fmt.Errorf("failed to create library manager: %w", err)
	}
	defer libManager.Close()

	// Manage HTTP sessions (1.1/2/chunked encoding/SSE)
	httpManager, err := http.NewSessionManager(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create HTTP session manager: %w", err)
	}
	defer httpManager.Close()

	// Manage filesystem (stdio) sessions for JSON aggregation
	fsManager, err := fs.NewSessionManager(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create FS session manager: %w", err)
	}
	defer fsManager.Close()

	if !tui {
		consoleDisplay.PrintInfo("Loading eBPF programs...")
	}

	if err := loader.Load(); err != nil {
		return fmt.Errorf("failed to load eBPF programs: %w", err)
	}

	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()

	// Start event processing
	if err := loader.Start(ctx); err != nil {
		return fmt.Errorf("failed to start event processing: %w", err)
	}

	// Enumerate all libraries for TLS inspection
	logrus.Debug("Doing initial enumeration of libraries for TLS inspection")
	if err := loader.RunIterLibEnum(); err != nil {
		return fmt.Errorf("failed to enumerate libraries: %w", err)
	}

	if !tui {
		consoleDisplay.PrintInfo("Monitoring MCP communication... Press Ctrl+C to stop")
		consoleDisplay.PrintInfo("")
	}

	// Create MCP parser and statistics
	parser, err := mcp.NewParser(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create MCP parser: %w", err)
	}
	defer parser.Close()

	// Run TUI or wait for context cancellation
	if tui {
		// Run TUI in a goroutine, cancel context when TUI exits
		go func() {
			if err := tuiDisplay.Run(); err != nil {
				logrus.WithError(err).Error("TUI error")
			}
			cancel()
		}()
	}

	// The main loop starts in loader.Start().
	// Waiting for context cancellation (Ctrl+C or TUI exit).
	<-ctx.Done()

	return nil
}
