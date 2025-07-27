package ebpf

import (
	"github.com/alex-ilgayev/mcpspy/pkg/encoder"
)

type EventType uint8

const (
	EventTypeRead    EventType = 1
	EventTypeWrite   EventType = 2
	EventTypeLibrary EventType = 3
	EventTypeHTTP    EventType = 4
)

// Event is the interface for all events
type Event interface {
	Type() EventType
}

// EventHeader represents the common header for all events
type EventHeader struct {
	EventType EventType
	_         [3]uint8 // padding
	PID       uint32
	CommBytes [16]uint8
}

func (h *EventHeader) Comm() string {
	return encoder.BytesToStr(h.CommBytes[:])
}

// DataEvent represents the r/w payload which
// contains the mcp message.
type DataEvent struct {
	EventHeader

	Size    uint32           // Actual data size
	BufSize uint32           // Size of data in buf (may be truncated)
	Buf     [16 * 1024]uint8 // Data buffer
}

func (e *DataEvent) Type() EventType { return e.EventType }

// LibraryEvent represents a new loaded library in memory.
// used for uprobe hooking for tls inspection
type LibraryEvent struct {
	EventHeader
	PathBytes [512]uint8
}

func (e *LibraryEvent) Type() EventType { return e.EventType }
func (e *LibraryEvent) Path() string {
	return encoder.BytesToStr(e.PathBytes[:])
}

// HTTPPayload represents HTTP traffic captured by ecapture
type HTTPPayload struct {
	EventHeader

	IsRequest  bool   // true for request, false for response
	Method     string // GET, POST, etc (for requests)
	URL        string // URL path (for requests)
	StatusCode int    // Status code (for responses)
	Headers    map[string]string
	Body       []byte
}

func (e *HTTPPayload) Type() EventType { return EventTypeHTTP }
