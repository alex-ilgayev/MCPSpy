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
	"github.com/alex-ilgayev/mcpspy/pkg/encoder"
	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
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

	consoleDisplay.PrintInfo("Monitoring MCP communication... Press Ctrl+C to stop")
	consoleDisplay.PrintInfo("")

	// Create MCP parser and statistics
	parser := mcp.NewParser()
	stats := make(map[string]int)

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

			// Get buffer data
			buf := event.Buf[:event.BufSize]
			if len(buf) == 0 {
				continue
			}

			commStr := encoder.BytesToStr(event.Comm[:])

			// Parse raw eBPF event data into MCP messages
			messages, err := parser.ParseData(buf, event.EventType, event.PID, commStr)
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
		}
	}
}
