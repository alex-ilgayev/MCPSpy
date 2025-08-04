package userland

/*
#cgo CFLAGS: -I../../userland
#cgo LDFLAGS: -L../../userland -lmcpspy -ldl -lpthread

#include <stdlib.h>
#include <string.h>
#include "libmcpspy.h"

// Wrapper functions to handle Go-C interface
int start_monitoring_wrapper(char* config_json) {
    return mcpspy_start_monitoring(config_json);
}

int stop_monitoring_wrapper() {
    return mcpspy_stop_monitoring();
}

int get_next_event_wrapper(mcp_event_t* event, int timeout_ms) {
    return mcpspy_get_next_event(event, timeout_ms);
}
*/
import "C"
import (
	"fmt"
	"time"
	"unsafe"
)

// CGOEvent represents an MCP event from the C library
type CGOEvent struct {
	Timestamp   time.Time
	PID         uint32
	Comm        string
	Transport   string
	EventType   string
	FD          int
	Size        uint64
	BufSize     uint64
	Data        []byte
}

// CGOMonitor wraps the C library for userland monitoring
type CGOMonitor struct {
	running bool
	events  chan *CGOEvent
	config  *Config
}

// NewCGOMonitor creates a new CGO-based monitor
func NewCGOMonitor(config *Config) *CGOMonitor {
	return &CGOMonitor{
		running: false,
		events:  make(chan *CGOEvent, 1000),
		config:  config,
	}
}

// Start begins monitoring using the C library
func (m *CGOMonitor) Start() error {
	if m.running {
		return fmt.Errorf("monitor already running")
	}

	// Convert Go config to JSON for C library
	// For now, use empty config - TODO: implement proper JSON marshaling
	configJSON := C.CString("{}")
	defer C.free(unsafe.Pointer(configJSON))

	// Start C library monitoring
	result := C.start_monitoring_wrapper(configJSON)
	if result != 0 {
		return fmt.Errorf("failed to start C library monitoring: %d", result)
	}

	m.running = true

	// Start event polling goroutine
	go m.eventLoop()

	return nil
}

// Stop stops the monitoring
func (m *CGOMonitor) Stop() error {
	if !m.running {
		return nil
	}

	m.running = false

	// Stop C library monitoring
	result := C.stop_monitoring_wrapper()
	if result != 0 {
		return fmt.Errorf("failed to stop C library monitoring: %d", result)
	}

	close(m.events)
	return nil
}

// Events returns the events channel
func (m *CGOMonitor) Events() <-chan *CGOEvent {
	return m.events
}

// eventLoop polls events from the C library
func (m *CGOMonitor) eventLoop() {
	defer close(m.events)

	for m.running {
		var cEvent C.mcp_event_t

		// Poll for next event with 100ms timeout
		result := C.get_next_event_wrapper(&cEvent, 100)
		
		switch result {
		case 1: // Event available
			goEvent := m.convertCEventToGo(&cEvent)
			select {
			case m.events <- goEvent:
			case <-time.After(time.Millisecond * 10):
				// Drop event if channel is full
			}
		case 0: // Timeout or no events
			continue
		case -1: // Error
			return
		}
	}
}

// convertCEventToGo converts a C event to a Go event structure
func (m *CGOMonitor) convertCEventToGo(cEvent *C.mcp_event_t) *CGOEvent {
	goEvent := &CGOEvent{
		Timestamp:  time.Unix(int64(cEvent.timestamp), 0),
		PID:        uint32(cEvent.pid),
		Comm:       C.GoString(&cEvent.comm[0]),
		Transport:  m.transportTypeToString(int(cEvent.transport)),
		EventType:  m.eventTypeToString(int(cEvent.event_type)),
		FD:         int(cEvent.fd),
		Size:       uint64(cEvent.size),
		BufSize:    uint64(cEvent.buf_size),
	}

	// Copy buffer data
	if cEvent.buf_size > 0 {
		goEvent.Data = C.GoBytes(unsafe.Pointer(&cEvent.buf[0]), C.int(cEvent.buf_size))
	}

	return goEvent
}

// transportTypeToString converts C transport type to string
func (m *CGOMonitor) transportTypeToString(transportType int) string {
	switch transportType {
	case 1: // TRANSPORT_STDIO
		return "stdio"
	default:
		return "unknown"
	}
}

// eventTypeToString converts C event type to string
func (m *CGOMonitor) eventTypeToString(eventType int) string {
	switch eventType {
	case 1: // EVENT_TYPE_READ
		return "read"
	case 2: // EVENT_TYPE_WRITE
		return "write"
	default:
		return "unknown"
	}
}

// LaunchWithLD_PRELOAD launches a command with LD_PRELOAD set to the MCPSpy library
func (m *CGOMonitor) LaunchWithLD_PRELOAD(command string, args []string, libPath string) error {
	// This would implement launching a process with LD_PRELOAD
	// For now, return not implemented
	return fmt.Errorf("LD_PRELOAD launch not yet implemented")
}

// Close cleans up the monitor
func (m *CGOMonitor) Close() error {
	return m.Stop()
}