package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
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
	rootCmd.Flags().BoolVarP(&showBuffers, "buffers", "b", false, "Show raw message buffers")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file (JSONL format will be written to file)")
	rootCmd.Flags().StringVarP(&logLevel, "log-level", "l", "info", "Set log level (trace, debug, info, warn, error, fatal, panic)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
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

	// Set up console display (always show console output)
	consoleDisplay, err := output.NewConsoleDisplay(os.Stdout, showBuffers, eventBus)
	if err != nil {
		return fmt.Errorf("failed to create console display: %w", err)
	}
	consoleDisplay.PrintHeader()

	// Set up file output if specified
	if outputFile != "" {
		file, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file '%s': %w", outputFile, err)
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

	consoleDisplay.PrintInfo("Loading eBPF programs...")
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

	consoleDisplay.PrintInfo("Monitoring MCP communication... Press Ctrl+C to stop")
	consoleDisplay.PrintInfo("")

	// Create MCP parser and statistics
	parser, err := mcp.NewParser(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create MCP parser: %w", err)
	}
	defer parser.Close()

	// The main loop starts in loader.Start().
	// Waiting for context cancellation (Ctrl+C).
	<-ctx.Done()

	return nil
}
