package output

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/fatih/color"
)

// DebugFilterConfig holds filter configuration for debug output
type DebugFilterConfig struct {
	EventTypes  map[event.EventType]bool // nil or empty = all
	PID         uint32                   // 0 = all PIDs
	Comm        string                   // "" = all processes
	Host        string                   // "" = all hosts, regex pattern
	ShowPayload bool                     // Show payload/buffer content
}

// DebugDisplay handles debug output formatting
type DebugDisplay struct {
	writer    io.Writer
	eventBus  bus.EventBus
	config    DebugFilterConfig
	hostRegex *regexp.Regexp               // Compiled host regex (nil = no filter)
	stats     map[event.EventType]*uint64  // Atomic counters per event type
	mu        sync.Mutex                   // Protects writer access
}

// Colors for debug output
var (
	debugTimestampColor = color.New(color.FgHiBlack)
	debugEventTypeColor = color.New(color.FgCyan, color.Bold)
	debugPIDColor       = color.New(color.FgYellow)
	debugCommColor      = color.New(color.FgGreen)
	debugPayloadColor   = color.New(color.FgWhite)
	debugAlertColor     = color.New(color.FgRed, color.Bold)
	debugMethodColor    = color.New(color.FgMagenta)
)

// NewDebugDisplay creates a new debug display handler
func NewDebugDisplay(writer io.Writer, eventBus bus.EventBus, config DebugFilterConfig) (*DebugDisplay, error) {
	d := &DebugDisplay{
		writer:   writer,
		eventBus: eventBus,
		config:   config,
		stats:    make(map[event.EventType]*uint64),
	}

	// Compile host regex if provided
	if config.Host != "" {
		regex, err := regexp.Compile(config.Host)
		if err != nil {
			return nil, fmt.Errorf("invalid host regex '%s': %w", config.Host, err)
		}
		d.hostRegex = regex
	}

	// Initialize stats counters for all event types
	for _, et := range allEventTypes() {
		counter := uint64(0)
		d.stats[et] = &counter
	}

	// Subscribe to all event types
	subscriptions := []struct {
		eventType event.EventType
		handler   bus.EventProcessor
	}{
		{event.EventTypeFSRead, d.handleFSDataEvent},
		{event.EventTypeFSWrite, d.handleFSDataEvent},
		{event.EventTypeLibrary, d.handleLibraryEvent},
		{event.EventTypeTlsPayloadSend, d.handleTLSPayloadEvent},
		{event.EventTypeTlsPayloadRecv, d.handleTLSPayloadEvent},
		{event.EventTypeTlsFree, d.handleTLSFreeEvent},
		{event.EventTypeHttpRequest, d.handleHttpRequestEvent},
		{event.EventTypeHttpResponse, d.handleHttpResponseEvent},
		{event.EventTypeHttpSSE, d.handleSSEEvent},
		{event.EventTypeMCPMessage, d.handleMCPEvent},
		{event.EventTypeFSAggregatedRead, d.handleFSAggregatedEvent},
		{event.EventTypeFSAggregatedWrite, d.handleFSAggregatedEvent},
		{event.EventTypeSecurityAlert, d.handleSecurityAlertEvent},
		{event.EventTypeLLMMessage, d.handleLLMEvent},
		{event.EventTypeToolUsage, d.handleToolUsageEvent},
	}

	for _, sub := range subscriptions {
		if err := eventBus.Subscribe(sub.eventType, sub.handler); err != nil {
			return nil, fmt.Errorf("failed to subscribe to %s: %w", sub.eventType, err)
		}
	}

	return d, nil
}

// Close is a no-op for now (event bus Close() handles cleanup)
func (d *DebugDisplay) Close() {}

// PrintHeader prints debug mode header
func (d *DebugDisplay) PrintHeader() {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Fprintln(d.writer, strings.Repeat("=", 80))
	fmt.Fprintln(d.writer, "MCPSpy Debug Mode")
	fmt.Fprintln(d.writer, strings.Repeat("=", 80))
}

// PrintFilters prints the active filters
func (d *DebugDisplay) PrintFilters() {
	d.mu.Lock()
	defer d.mu.Unlock()

	var eventList string
	if len(d.config.EventTypes) == 0 {
		eventList = "[all]"
	} else {
		names := make([]string, 0, len(d.config.EventTypes))
		for et := range d.config.EventTypes {
			names = append(names, et.String())
		}
		eventList = strings.Join(names, ",")
	}

	fmt.Fprintf(d.writer, "Filters: events=%s pid=%d comm=%q host=%q payload=%v\n\n",
		eventList, d.config.PID, d.config.Comm, d.config.Host, d.config.ShowPayload)
}

// PrintStats prints event statistics
func (d *DebugDisplay) PrintStats() {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Fprintln(d.writer, "\n"+strings.Repeat("=", 80))
	fmt.Fprintln(d.writer, "Event Statistics:")
	fmt.Fprintln(d.writer, strings.Repeat("-", 40))

	total := uint64(0)
	for _, et := range allEventTypes() {
		count := atomic.LoadUint64(d.stats[et])
		if count > 0 {
			fmt.Fprintf(d.writer, "  %-25s %d\n", et.String(), count)
			total += count
		}
	}
	fmt.Fprintln(d.writer, strings.Repeat("-", 40))
	fmt.Fprintf(d.writer, "  %-25s %d\n", "TOTAL", total)
}

// shouldDisplay checks if event passes all filters
func (d *DebugDisplay) shouldDisplay(et event.EventType, pid uint32, comm string, host string) bool {
	// Event type filter
	if len(d.config.EventTypes) > 0 && !d.config.EventTypes[et] {
		return false
	}

	// PID filter
	if d.config.PID != 0 && pid != d.config.PID {
		return false
	}

	// Comm filter (substring match, case-insensitive)
	if d.config.Comm != "" && !strings.Contains(strings.ToLower(comm), strings.ToLower(d.config.Comm)) {
		return false
	}

	// Host filter (regex match)
	if d.hostRegex != nil && !d.hostRegex.MatchString(host) {
		return false
	}

	return true
}

// formatTimestamp formats timestamp for display
func (d *DebugDisplay) formatTimestamp(t time.Time) string {
	return debugTimestampColor.Sprint(t.Format("15:04:05.000"))
}

// formatEventType formats event type for display
func (d *DebugDisplay) formatEventType(et event.EventType) string {
	return debugEventTypeColor.Sprintf("%-20s", et.String())
}

// formatPID formats PID for display
func (d *DebugDisplay) formatPID(pid uint32) string {
	return debugPIDColor.Sprintf("[%d]", pid)
}

// formatComm formats comm for display
func (d *DebugDisplay) formatComm(comm string) string {
	return debugCommColor.Sprintf("%-15s", truncateComm(comm, 15))
}

// printEventLine prints a single event line (thread-safe)
func (d *DebugDisplay) printEventLine(et event.EventType, timestamp time.Time, pid uint32, comm string, details string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Fprintf(d.writer, "%s %s %s %s %s\n",
		d.formatTimestamp(timestamp),
		d.formatEventType(et),
		d.formatPID(pid),
		d.formatComm(comm),
		details,
	)
}

// printPayload prints payload if configured (must be called with lock held or after printEventLine)
func (d *DebugDisplay) printPayload(data []byte) {
	if !d.config.ShowPayload || len(data) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Try to pretty-print JSON
	var prettyContent string
	var jsonObj interface{}

	if err := json.Unmarshal(data, &jsonObj); err == nil {
		if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
			prettyContent = string(prettyBytes)
		} else {
			prettyContent = string(data)
		}
	} else {
		prettyContent = string(data)
	}

	fmt.Fprintln(d.writer, debugPayloadColor.Sprint(prettyContent))
}

// --- Event Handlers ---

func (d *DebugDisplay) handleFSDataEvent(e event.Event) {
	fsEvent, ok := e.(*event.FSDataEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[fsEvent.EventType], 1)

	if !d.shouldDisplay(fsEvent.EventType, fsEvent.PID, fsEvent.Comm(), "") {
		return
	}

	details := fmt.Sprintf("inode=%d size=%d from=%s[%d] to=%s[%d]",
		fsEvent.Inode, fsEvent.Size,
		fsEvent.FromCommStr(), fsEvent.FromPID,
		fsEvent.ToCommStr(), fsEvent.ToPID,
	)

	d.printEventLine(fsEvent.EventType, time.Now(), fsEvent.PID, fsEvent.Comm(), details)
	d.printPayload(fsEvent.Buffer())
}

func (d *DebugDisplay) handleLibraryEvent(e event.Event) {
	libEvent, ok := e.(*event.LibraryEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeLibrary], 1)

	if !d.shouldDisplay(event.EventTypeLibrary, libEvent.PID, libEvent.Comm(), "") {
		return
	}

	details := fmt.Sprintf("path=%s inode=%d mnt_ns=%d",
		libEvent.Path(), libEvent.Inode, libEvent.MntNSID,
	)

	d.printEventLine(event.EventTypeLibrary, time.Now(), libEvent.PID, libEvent.Comm(), details)
}

func (d *DebugDisplay) handleTLSPayloadEvent(e event.Event) {
	tlsEvent, ok := e.(*event.TlsPayloadEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[tlsEvent.EventType], 1)

	if !d.shouldDisplay(tlsEvent.EventType, tlsEvent.PID, tlsEvent.Comm(), "") {
		return
	}

	details := fmt.Sprintf("ssl_ctx=0x%x size=%d http=%s",
		tlsEvent.SSLContext, tlsEvent.Size, tlsEvent.HttpVersion.String(),
	)

	d.printEventLine(tlsEvent.EventType, time.Now(), tlsEvent.PID, tlsEvent.Comm(), details)
	d.printPayload(tlsEvent.Buffer())
}

func (d *DebugDisplay) handleTLSFreeEvent(e event.Event) {
	tlsFreeEvent, ok := e.(*event.TlsFreeEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeTlsFree], 1)

	if !d.shouldDisplay(event.EventTypeTlsFree, tlsFreeEvent.PID, tlsFreeEvent.Comm(), "") {
		return
	}

	details := fmt.Sprintf("ssl_ctx=0x%x", tlsFreeEvent.SSLContext)

	d.printEventLine(event.EventTypeTlsFree, time.Now(), tlsFreeEvent.PID, tlsFreeEvent.Comm(), details)
}

func (d *DebugDisplay) handleHttpRequestEvent(e event.Event) {
	httpEvent, ok := e.(*event.HttpRequestEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeHttpRequest], 1)

	if !d.shouldDisplay(event.EventTypeHttpRequest, httpEvent.PID, httpEvent.Comm(), httpEvent.Host+httpEvent.Path) {
		return
	}

	details := fmt.Sprintf("%s %s%s ssl_ctx=0x%x",
		debugMethodColor.Sprint(httpEvent.Method),
		httpEvent.Host,
		httpEvent.Path,
		httpEvent.SSLContext,
	)

	d.printEventLine(event.EventTypeHttpRequest, time.Now(), httpEvent.PID, httpEvent.Comm(), details)
	d.printPayload(httpEvent.RequestPayload)
}

func (d *DebugDisplay) handleHttpResponseEvent(e event.Event) {
	httpEvent, ok := e.(*event.HttpResponseEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeHttpResponse], 1)

	if !d.shouldDisplay(event.EventTypeHttpResponse, httpEvent.PID, httpEvent.Comm(), httpEvent.Host+httpEvent.Path) {
		return
	}

	details := fmt.Sprintf("%s %s%s -> %d chunked=%v ssl_ctx=0x%x",
		debugMethodColor.Sprint(httpEvent.Method),
		httpEvent.Host,
		httpEvent.Path,
		httpEvent.Code,
		httpEvent.IsChunked,
		httpEvent.SSLContext,
	)

	d.printEventLine(event.EventTypeHttpResponse, time.Now(), httpEvent.PID, httpEvent.Comm(), details)
	d.printPayload(httpEvent.ResponsePayload)
}

func (d *DebugDisplay) handleSSEEvent(e event.Event) {
	sseEvent, ok := e.(*event.SSEEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeHttpSSE], 1)

	if !d.shouldDisplay(event.EventTypeHttpSSE, sseEvent.PID, sseEvent.Comm(), sseEvent.Host+sseEvent.Path) {
		return
	}

	details := fmt.Sprintf("SSE %s%s event=%s ssl_ctx=0x%x",
		sseEvent.Host, sseEvent.Path, sseEvent.SSEEventType, sseEvent.SSLContext,
	)

	d.printEventLine(event.EventTypeHttpSSE, time.Now(), sseEvent.PID, sseEvent.Comm(), details)
	d.printPayload(sseEvent.Data)
}

func (d *DebugDisplay) handleMCPEvent(e event.Event) {
	mcpEvent, ok := e.(*event.MCPEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeMCPMessage], 1)

	// Determine PID/comm/host for filtering
	var pid uint32
	var comm string
	var host string
	var transport string

	if mcpEvent.TransportType == event.TransportTypeStdio && mcpEvent.StdioTransport != nil {
		pid = mcpEvent.StdioTransport.FromPID
		comm = mcpEvent.StdioTransport.FromComm
		transport = "stdio"
	} else if mcpEvent.HttpTransport != nil {
		pid = mcpEvent.HttpTransport.PID
		comm = mcpEvent.HttpTransport.Comm
		host = mcpEvent.HttpTransport.Host
		transport = "http"
	}

	if !d.shouldDisplay(event.EventTypeMCPMessage, pid, comm, host) {
		return
	}

	details := fmt.Sprintf("%s id=%v %s transport=%s",
		debugMethodColor.Sprint(mcpEvent.MessageType),
		mcpEvent.ID,
		debugMethodColor.Sprint(mcpEvent.Method),
		transport,
	)

	d.printEventLine(event.EventTypeMCPMessage, mcpEvent.Timestamp, pid, comm, details)
	d.printPayload([]byte(mcpEvent.Raw))
}

func (d *DebugDisplay) handleFSAggregatedEvent(e event.Event) {
	aggEvent, ok := e.(*event.FSAggregatedEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[aggEvent.EventType], 1)

	if !d.shouldDisplay(aggEvent.EventType, aggEvent.PID, aggEvent.Comm(), "") {
		return
	}

	details := fmt.Sprintf("inode=%d size=%d from=%s[%d] to=%s[%d]",
		aggEvent.Inode, len(aggEvent.Payload),
		aggEvent.FromCommStr(), aggEvent.FromPID,
		aggEvent.ToCommStr(), aggEvent.ToPID,
	)

	d.printEventLine(aggEvent.EventType, time.Now(), aggEvent.PID, aggEvent.Comm(), details)
	d.printPayload(aggEvent.Payload)
}

func (d *DebugDisplay) handleSecurityAlertEvent(e event.Event) {
	alertEvent, ok := e.(*event.SecurityAlertEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeSecurityAlert], 1)

	var pid uint32
	var comm string
	var host string

	if alertEvent.MCPEvent != nil {
		if alertEvent.MCPEvent.StdioTransport != nil {
			pid = alertEvent.MCPEvent.StdioTransport.FromPID
			comm = alertEvent.MCPEvent.StdioTransport.FromComm
		} else if alertEvent.MCPEvent.HttpTransport != nil {
			pid = alertEvent.MCPEvent.HttpTransport.PID
			comm = alertEvent.MCPEvent.HttpTransport.Comm
			host = alertEvent.MCPEvent.HttpTransport.Host
		}
	}

	if !d.shouldDisplay(event.EventTypeSecurityAlert, pid, comm, host) {
		return
	}

	method := ""
	if alertEvent.MCPEvent != nil {
		method = alertEvent.MCPEvent.Method
	}

	details := debugAlertColor.Sprintf("ALERT risk=%s score=%.2f category=%s method=%s",
		alertEvent.RiskLevel, alertEvent.RiskScore, alertEvent.Category, method,
	)

	d.printEventLine(event.EventTypeSecurityAlert, alertEvent.Timestamp, pid, comm, details)
}

func (d *DebugDisplay) handleLLMEvent(e event.Event) {
	llmEvent, ok := e.(*event.LLMEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeLLMMessage], 1)

	if !d.shouldDisplay(event.EventTypeLLMMessage, llmEvent.PID, llmEvent.Comm, llmEvent.Host+llmEvent.Path) {
		return
	}

	details := fmt.Sprintf("%s %s%s model=%s",
		debugMethodColor.Sprint(llmEvent.MessageType),
		llmEvent.Host,
		llmEvent.Path,
		llmEvent.Model,
	)

	d.printEventLine(event.EventTypeLLMMessage, llmEvent.Timestamp, llmEvent.PID, llmEvent.Comm, details)

	if d.config.ShowPayload && llmEvent.Content != "" {
		d.printPayload([]byte(llmEvent.Content))
	}
}

func (d *DebugDisplay) handleToolUsageEvent(e event.Event) {
	toolEvent, ok := e.(*event.ToolUsageEvent)
	if !ok {
		return
	}

	atomic.AddUint64(d.stats[event.EventTypeToolUsage], 1)

	if !d.shouldDisplay(event.EventTypeToolUsage, toolEvent.PID, toolEvent.Comm, toolEvent.Host) {
		return
	}

	details := fmt.Sprintf("%s tool=%s id=%s host=%s",
		debugMethodColor.Sprint(toolEvent.UsageType),
		toolEvent.ToolName,
		toolEvent.ToolID,
		toolEvent.Host,
	)

	d.printEventLine(event.EventTypeToolUsage, toolEvent.Timestamp, toolEvent.PID, toolEvent.Comm, details)

	if d.config.ShowPayload {
		// Prefer RawJSON which contains the complete tool block
		if toolEvent.RawJSON != "" {
			d.printPayload([]byte(toolEvent.RawJSON))
		} else {
			// Fallback to Input/Output for backwards compatibility
			if toolEvent.Input != "" {
				d.printPayload([]byte(toolEvent.Input))
			}
			if toolEvent.Output != "" {
				d.printPayload([]byte(toolEvent.Output))
			}
		}
	}
}

// Helper functions

func allEventTypes() []event.EventType {
	return []event.EventType{
		event.EventTypeFSRead,
		event.EventTypeFSWrite,
		event.EventTypeLibrary,
		event.EventTypeTlsPayloadSend,
		event.EventTypeTlsPayloadRecv,
		event.EventTypeTlsFree,
		event.EventTypeHttpRequest,
		event.EventTypeHttpResponse,
		event.EventTypeHttpSSE,
		event.EventTypeMCPMessage,
		event.EventTypeFSAggregatedRead,
		event.EventTypeFSAggregatedWrite,
		event.EventTypeSecurityAlert,
		event.EventTypeLLMMessage,
		event.EventTypeToolUsage,
	}
}

func truncateComm(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// ParseEventTypeName converts event type name string to EventType
func ParseEventTypeName(name string) (event.EventType, bool) {
	nameToType := map[string]event.EventType{
		"fs_read":             event.EventTypeFSRead,
		"fs_write":            event.EventTypeFSWrite,
		"library":             event.EventTypeLibrary,
		"tls_send":            event.EventTypeTlsPayloadSend,
		"tls_recv":            event.EventTypeTlsPayloadRecv,
		"tls_free":            event.EventTypeTlsFree,
		"http_request":        event.EventTypeHttpRequest,
		"http_response":       event.EventTypeHttpResponse,
		"http_sse":            event.EventTypeHttpSSE,
		"mcp_message":         event.EventTypeMCPMessage,
		"fs_aggregated_read":  event.EventTypeFSAggregatedRead,
		"fs_aggregated_write": event.EventTypeFSAggregatedWrite,
		"security_alert":      event.EventTypeSecurityAlert,
		"llm_message":         event.EventTypeLLMMessage,
		"tool_usage":          event.EventTypeToolUsage,
	}
	et, ok := nameToType[strings.ToLower(name)]
	return et, ok
}

// AllEventTypeNames returns all valid event type names
func AllEventTypeNames() []string {
	return []string{
		"fs_read", "fs_write", "library",
		"tls_send", "tls_recv", "tls_free",
		"http_request", "http_response", "http_sse",
		"mcp_message", "fs_aggregated_read", "fs_aggregated_write",
		"security_alert", "llm_message", "tool_usage",
	}
}
