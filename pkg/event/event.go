package event

import (
	"github.com/alex-ilgayev/mcpspy/pkg/encoder"
)

type EventType uint8

const (
	EventTypeFSRead         EventType = 1
	EventTypeFSWrite        EventType = 2
	EventTypeLibrary        EventType = 3
	EventTypeTlsPayloadSend EventType = 4
	EventTypeTlsPayloadRecv EventType = 5
	EventTypeTlsFree        EventType = 6

	// Event that is not originated from eBPF
	EventTypeHttp EventType = 100
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
	case EventTypeTlsPayloadSend:
		return "tls_send"
	case EventTypeTlsPayloadRecv:
		return "tls_recv"
	case EventTypeTlsFree:
		return "tls_free"
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

// FSDataEvent represents the r/w payload which
// contains the mcp message.
type FSDataEvent struct {
	EventHeader

	Size    uint32           // Actual data size
	BufSize uint32           // Size of data in buf (may be truncated)
	Buf     [16 * 1024]uint8 // Data buffer
}

func (e *FSDataEvent) Type() EventType { return e.EventType }

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
// we need to treat it differently, as it consist
// of HTTP data, and not neccesarily MCP data.
type TlsPayloadEvent struct {
	EventHeader

	SSLContext  uint64           // SSL context pointer (session identifier)
	Size        uint32           // Actual data size
	BufSize     uint32           // Size of data in buf (may be truncated)
	HttpVersion HttpVersion      // Identified HTTP version
	Buf         [16 * 1024]uint8 // Data buffer
}

func (e *TlsPayloadEvent) Type() EventType { return e.EventType }
func (e *TlsPayloadEvent) Buffer() []byte {
	return e.Buf[:e.BufSize]
}

type TlsFreeEvent struct {
	EventHeader

	SSLContext uint64
}

func (e *TlsFreeEvent) Type() EventType { return e.EventType }

// HttpEvent is generated after aggregating TLS events.
// (not generated from eBPF program)
type HttpEvent struct {
	EventHeader

	SSLContext uint64

	// Request
	Method         string
	Host           string
	Path           string
	RequestHeaders map[string]string
	RequestPayload []byte

	// Response
	ResponseHeaders map[string]string
	Code            int
	IsChunked       bool
	ResponsePayload []byte
}

func (e *HttpEvent) Type() EventType { return e.EventType }
