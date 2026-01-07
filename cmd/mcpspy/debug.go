package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/fs"
	"github.com/alex-ilgayev/mcpspy/pkg/http"
	"github.com/alex-ilgayev/mcpspy/pkg/llm"
	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
	"github.com/alex-ilgayev/mcpspy/pkg/namespace"
	"github.com/alex-ilgayev/mcpspy/pkg/output"
)

// Debug command flags
var (
	debugEventTypes  []string // Filter by event type names
	debugPID         uint32   // Filter by specific PID
	debugComm        string   // Filter by process name (comm)
	debugHost        string   // Filter by host (regex)
	debugShowPayload bool     // Show payload/buffer data
)

func newDebugCmd() *cobra.Command {
	debugCmd := &cobra.Command{
		Use:   "debug",
		Short: "Debug mode: show all events with filtering",
		Long: `Debug mode displays raw and derived events for troubleshooting.

Use this mode to debug HTTP parsing issues, inspect raw messages for new
protocol support, or understand the event flow through the system.

Event Types (Raw eBPF):
  fs_read             - Filesystem read operations
  fs_write            - Filesystem write operations
  library             - Library load events
  tls_send            - TLS payload send
  tls_recv            - TLS payload receive
  tls_free            - TLS context free

Event Types (Derived):
  http_request        - HTTP request parsed
  http_response       - HTTP response parsed
  http_sse            - HTTP Server-Sent Event
  mcp_message         - MCP JSON-RPC message
  fs_aggregated_read  - Aggregated FS read (complete JSON)
  fs_aggregated_write - Aggregated FS write (complete JSON)
  security_alert      - Security/injection alert
  llm_message         - LLM API message
  tool_usage          - Tool usage event

Examples:
  # Show all events
  sudo mcpspy debug

  # Show only MCP messages
  sudo mcpspy debug --events mcp_message

  # Show MCP messages with payloads
  sudo mcpspy debug --events mcp_message --payload

  # Filter by PID
  sudo mcpspy debug --pid 12345

  # Filter by process name
  sudo mcpspy debug --comm claude

  # Debug HTTP parsing issues
  sudo mcpspy debug --events tls_recv,http_request,http_response --payload

  # Filter by host (regex)
  sudo mcpspy debug --host "api\.anthropic\.com" --payload

  # Combine filters
  sudo mcpspy debug --events mcp_message,http_request --pid 12345 --payload`,
		RunE:         runDebug,
		SilenceUsage: true,
	}

	// Filter flags
	debugCmd.Flags().StringSliceVarP(&debugEventTypes, "events", "e", nil,
		"Filter by event type(s), comma-separated (e.g., 'mcp_message,http_request')")
	debugCmd.Flags().Uint32VarP(&debugPID, "pid", "p", 0,
		"Filter by specific PID")
	debugCmd.Flags().StringVarP(&debugComm, "comm", "c", "",
		"Filter by process name (substring match)")
	debugCmd.Flags().StringVar(&debugHost, "host", "",
		"Filter by host (regex pattern, e.g., 'api\\.anthropic\\.com')")
	debugCmd.Flags().BoolVar(&debugShowPayload, "payload", false,
		"Show payload/buffer data for events")

	return debugCmd
}

func runDebug(cmd *cobra.Command, args []string) error {
	// Set log level to warn for debug mode (reduce noise from internal logging)
	logrus.SetLevel(logrus.WarnLevel)

	// Build event type filter set
	eventTypeFilter := buildEventTypeFilter()

	// Build filter config
	filterConfig := output.DebugFilterConfig{
		EventTypes:  eventTypeFilter,
		PID:         debugPID,
		Comm:        debugComm,
		Host:        debugHost,
		ShowPayload: debugShowPayload,
	}

	// Fetch current mount namespace
	mountNS, err := namespace.GetCurrentMountNamespace()
	if err != nil {
		return fmt.Errorf("failed to get current mount namespace: %w", err)
	}

	// Create event bus
	eventBus := bus.New()
	defer eventBus.Close()

	// Create debug display
	debugDisplay, err := output.NewDebugDisplay(os.Stdout, eventBus, filterConfig)
	if err != nil {
		return fmt.Errorf("failed to create debug display: %w", err)
	}
	defer debugDisplay.Close()

	// Print header and filters
	debugDisplay.PrintHeader()
	debugDisplay.PrintFilters()

	// Create and load eBPF program
	loader, err := ebpf.New(uint32(os.Getpid()), eventBus)
	if err != nil {
		return fmt.Errorf("failed to create eBPF loader: %w", err)
	}
	defer loader.Close()

	// Process library events and create uprobe hooks
	libManager, err := ebpf.NewLibraryManager(eventBus, loader, mountNS)
	if err != nil {
		return fmt.Errorf("failed to create library manager: %w", err)
	}
	defer libManager.Close()

	// Manage HTTP sessions
	httpManager, err := http.NewSessionManager(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create HTTP session manager: %w", err)
	}
	defer httpManager.Close()

	// Manage filesystem (stdio) sessions
	fsManager, err := fs.NewSessionManager(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create FS session manager: %w", err)
	}
	defer fsManager.Close()

	fmt.Fprintln(os.Stdout, "Loading eBPF programs...")

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

	// Enumerate libraries for TLS inspection
	if err := loader.RunIterLibEnum(); err != nil {
		return fmt.Errorf("failed to enumerate libraries: %w", err)
	}

	// Create MCP parser for derived events
	parser, err := mcp.NewParser(eventBus)
	if err != nil {
		return fmt.Errorf("failed to create MCP parser: %w", err)
	}
	defer parser.Close()

	// Always enable LLM parser in debug mode (for complete event visibility)
	llmParser, err := llm.NewParserWithConfig(eventBus, llm.ParserConfig{
		PublishLLMEvents:  true,
		PublishToolEvents: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create LLM parser: %w", err)
	}
	defer llmParser.Close()

	fmt.Fprintln(os.Stdout, "Debug mode active. Press Ctrl+C to stop.")
	fmt.Fprintln(os.Stdout)

	// Wait for context cancellation
	<-ctx.Done()

	// Print statistics
	debugDisplay.PrintStats()

	return nil
}

// buildEventTypeFilter builds the set of event types to display
func buildEventTypeFilter() map[event.EventType]bool {
	if len(debugEventTypes) == 0 {
		// Empty = all events
		return nil
	}

	filter := make(map[event.EventType]bool)
	for _, name := range debugEventTypes {
		// Handle comma-separated values within a single flag value
		parts := strings.Split(name, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if et, ok := output.ParseEventTypeName(part); ok {
				filter[et] = true
			} else {
				fmt.Fprintf(os.Stderr, "Warning: unknown event type '%s', ignoring\n", part)
				fmt.Fprintf(os.Stderr, "Valid event types: %s\n", strings.Join(output.AllEventTypeNames(), ", "))
			}
		}
	}

	return filter
}
