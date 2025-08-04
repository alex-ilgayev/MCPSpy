package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
	"github.com/alex-ilgayev/mcpspy/pkg/output"
	"github.com/alex-ilgayev/mcpspy/pkg/userland"
	"github.com/alex-ilgayev/mcpspy/pkg/version"

	mcpspydebug "github.com/alex-ilgayev/mcpspy/pkg/debug"
)

// Command line flags
var (
	showBuffers bool
	verbose     bool
	outputFile  string
	logLevel    string
	mode        string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "mcpspy",
		Short: "Monitor Model Context Protocol communication",
		Long: `MCPSpy is a CLI utility that uses eBPF or userland monitoring to track MCP (Model Context Protocol) 
communication by analyzing JSON-RPC 2.0 messages across multiple transports.`,
		Version:      fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.Commit, version.Date),
		RunE:         run,
		SilenceUsage: true,
	}

	// Add flags
	rootCmd.Flags().BoolVarP(&showBuffers, "buffers", "b", false, "Show raw message buffers")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging (debug level)")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file (JSONL format will be written to file)")
	rootCmd.Flags().StringVarP(&logLevel, "log-level", "l", "info", "Set log level (trace, debug, info, warn, error, fatal, panic)")
	rootCmd.Flags().StringVarP(&mode, "mode", "m", "ebpf", "Monitoring mode: 'ebpf' (kernel-level) or 'userland' (LD_PRELOAD)")

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

	// Set up console display (always show console output)
	consoleDisplay := output.NewConsoleDisplay(os.Stdout, showBuffers)
	consoleDisplay.PrintHeader()

	// Set up file output if specified
	var fileDisplay output.OutputHandler

	if outputFile != "" {
		file, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file '%s': %w", outputFile, err)
		}
		fileDisplay = output.NewJSONLDisplay(file)
		defer func() {
			if err := file.Close(); err != nil {
				logrus.WithError(err).Error("Failed to close output file")
			}
		}()
	}

	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()

	// Create MCP parser and statistics
	parser := mcp.NewParser()
	stats := make(map[string]int)

	switch mode {
	case "userland":
		return runUserlandMode(ctx, consoleDisplay, fileDisplay, parser, stats, level)
	case "ebpf":
		return runEBPFMode(ctx, consoleDisplay, fileDisplay, parser, stats, level)
	default:
		return fmt.Errorf("invalid mode '%s': must be 'ebpf' or 'userland'", mode)
	}
}

func runEBPFMode(ctx context.Context, consoleDisplay *output.ConsoleDisplay, fileDisplay output.OutputHandler, parser *mcp.Parser, stats map[string]int, level logrus.Level) error {
	// Setup trace pipe to debug eBPF programs if debug or trace level
	if level >= logrus.DebugLevel {
		go mcpspydebug.PrintTracePipe(logrus.StandardLogger())
	}

	// Create and load eBPF program
	loader, err := ebpf.New(level >= logrus.DebugLevel)
	if err != nil {
		return fmt.Errorf("failed to create eBPF loader: %w", err)
	}
	defer loader.Close()

	consoleDisplay.PrintInfo("Loading eBPF programs...")
	if err := loader.Load(); err != nil {
		return fmt.Errorf("failed to load eBPF programs: %w", err)
	}

	// Start event processing
	if err := loader.Start(ctx); err != nil {
		return fmt.Errorf("failed to start event processing: %w", err)
	}

	consoleDisplay.PrintInfo("Monitoring MCP communication with eBPF... Press Ctrl+C to stop")
	consoleDisplay.PrintInfo("")

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			consoleDisplay.PrintStats(stats)
			return nil

		case event, ok := <-loader.Events():
			if !ok {
				// Channel closed, exit
				consoleDisplay.PrintStats(stats)
				return nil
			}

			// Handle different event types
			switch e := event.(type) {
			case *ebpf.DataEvent:
				buf := e.Buf[:e.BufSize]
				if len(buf) == 0 {
					continue
				}

				// Parse raw eBPF event data into MCP messages
				messages, err := parser.ParseData(buf, e.EventType, e.PID, e.Comm())
				if err != nil {
					logrus.WithError(err).Debug("Failed to parse data")
					continue
				}

				// Update statistics
				for _, msg := range messages {
					if msg.Method != "" {
						stats[msg.Method]++
					}
				}

				// Display messages to console
				consoleDisplay.PrintMessages(messages)

				// Also write to file if specified
				if fileDisplay != nil {
					fileDisplay.PrintMessages(messages)
				}
			case *ebpf.LibraryEvent:
				// Handle library events - for now just log them
				logrus.WithFields(logrus.Fields{
					"pid":  e.PID,
					"comm": e.Comm(),
					"path": e.Path(),
				}).Trace("Library loaded")
			default:
				logrus.WithField("type", event.Type()).Warn("Unknown event type")
			}
		}
	}
}

func runUserlandMode(ctx context.Context, consoleDisplay *output.ConsoleDisplay, fileDisplay output.OutputHandler, parser *mcp.Parser, stats map[string]int, level logrus.Level) error {
	// Create userland monitor config with defaults
	config := &userland.Config{
		MonitorStdio:   true,   // Primary transport for MCP
		MonitorHTTP:    false,  // Disabled in simplified version
		MonitorSSL:     false,  // Disabled in simplified version  
		MonitorPackets: false,  // Disabled in simplified version
		LogLevel:       level,
	}

	// Create and start userland monitor
	monitor := userland.New(config)
	defer monitor.Close()

	if err := monitor.Start(); err != nil {
		return fmt.Errorf("failed to start userland monitor: %w", err)
	}

	consoleDisplay.PrintInfo("Monitoring MCP communication with userland mode (stdio)... Press Ctrl+C to stop")
	consoleDisplay.PrintInfo("")

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			consoleDisplay.PrintStats(stats)
			return nil

		case event, ok := <-monitor.Events():
			if !ok {
				// Channel closed, exit
				consoleDisplay.PrintStats(stats)
				return nil
			}

			// Handle different event types
			switch e := event.(type) {
			case *ebpf.DataEvent:
				buf := e.Buf[:e.BufSize]
				if len(buf) == 0 {
					continue
				}

				// Parse raw event data into MCP messages
				messages, err := parser.ParseData(buf, e.EventType, e.PID, e.Comm())
				if err != nil {
					logrus.WithError(err).Debug("Failed to parse data")
					continue
				}

				// Update statistics
				for _, msg := range messages {
					if msg.Method != "" {
						stats[msg.Method]++
					}
				}

				// Display messages to console
				consoleDisplay.PrintMessages(messages)

				// Also write to file if specified
				if fileDisplay != nil {
					fileDisplay.PrintMessages(messages)
				}
			default:
				logrus.WithField("type", fmt.Sprintf("%T", event)).Debug("Unknown event type in userland mode")
			}
		}
	}
}
