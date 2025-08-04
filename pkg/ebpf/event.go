package ebpf

import (
	"github.com/alex-ilgayev/mcpspy/pkg/encoder"
)

type EventType uint8

const (
	EventTypeFSRead  EventType = 1
	EventTypeFSWrite EventType = 2
	EventTypeLibrary EventType = 3
	EventTypeTlsSend EventType = 4
	EventTypeTlsRecv EventType = 5
)

type HttpVersion uint8

const (
	HttpVersionUnknown HttpVersion = 0
	HttpVersion1       HttpVersion = 1
	HttpVersion2       HttpVersion = 2
)

func (h HttpVersion) String() string {
	switch h {
	case HttpVersion1:
		return "http/1.1"
	case HttpVersion2:
		return "http/2"
	default:
		return "unknown"
	}
}

func (e EventType) String() string {
	switch e {
	case EventTypeFSRead:
		return "fs_read"
	case EventTypeFSWrite:
		return "fs_write"
	case EventTypeLibrary:
		return "library"
	case EventTypeTlsSend:
		return "tls_send"
	case EventTypeTlsRecv:
		return "tls_recv"
	default:
		return "unknown"
	}
}

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
	Inode     uint64
	PathBytes [512]uint8
}

func (e *LibraryEvent) Type() EventType { return e.EventType }
func (e *LibraryEvent) Path() string {
	return encoder.BytesToStr(e.PathBytes[:])
}

// Even though it's similar to DataEvent,
// we need to treat it differently, as it conasist
// of HTTP data, and not neccesarily MCP data.
type TlsEvent struct {
	EventHeader

	SSLContext  uint64           // SSL context pointer (session identifier)
	Size        uint32           // Actual data size
	BufSize     uint32           // Size of data in buf (may be truncated)
	HttpVersion HttpVersion      // Identified HTTP version
	Buf         [16 * 1024]uint8 // Data buffer
}

func (e *TlsEvent) Type() EventType { return e.EventType }
func (e *TlsEvent) Buffer() []byte {
	return e.Buf[:e.BufSize]
}
