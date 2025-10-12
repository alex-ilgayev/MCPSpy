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

	// Events that are not originated from eBPF

	// The request portion of the HTTP event was delievered.
	EventTypeHttpRequest EventType = 100
	// The response portion of the HTTP event was delievered.
	// (means http event is complete)
	EventTypeHttpResponse EventType = 101
	// Received an SSE event through an existing HTTP connection.
	EventTypeHttpSSE EventType = 102
	// Detected a parsed MCP message
	EventTypeMCPMessage EventType = 103
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
	case EventTypeHttpRequest:
		return "http_request"
	case EventTypeHttpResponse:
		return "http_response"
	case EventTypeHttpSSE:
		return "http_sse"
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

	Inode    uint32    // Inode number for correlation
	FromPID  uint32    // Sender (writer) PID
	FromComm [16]uint8 // Sender comm
	ToPID    uint32    // Receiver (reader) PID
	ToComm   [16]uint8 // Receiver comm

	Size    uint32           // Actual data size
	BufSize uint32           // Size of data in buf (may be truncated)
	Buf     [16 * 1024]uint8 // Data buffer
}

func (e *FSDataEvent) Type() EventType { return e.EventType }
func (e *FSDataEvent) FromCommStr() string {
	return encoder.BytesToStr(e.FromComm[:])
}
func (e *FSDataEvent) ToCommStr() string {
	return encoder.BytesToStr(e.ToComm[:])
}
func (e *FSDataEvent) Buffer() []byte {
	return e.Buf[:e.BufSize]
}

// LibraryEvent represents a new loaded library in memory.
// used for uprobe hooking for tls inspection
type LibraryEvent struct {
	EventHeader
	Inode     uint64
	MntNSID   uint32
	PathBytes [512]uint8
}

func (e *LibraryEvent) Type() EventType { return e.EventType }
func (e *LibraryEvent) Path() string {
	return encoder.BytesToStr(e.PathBytes[:])
}
func (e *LibraryEvent) MountNamespaceID() uint32 {
	return e.MntNSID
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

// HttpRequestEvent is generated after aggregating TLS events for a request.
// (not generated from eBPF program)
type HttpRequestEvent struct {
	EventHeader

	SSLContext uint64

	Method         string
	Host           string
	Path           string
	RequestHeaders map[string]string
	RequestPayload []byte
}

func (e *HttpRequestEvent) Type() EventType { return e.EventType }

// HttpResponseEvent is generated after aggregating TLS events for a response.
// (not generated from eBPF program)
type HttpResponseEvent struct {
	EventHeader
	HttpRequestEvent

	SSLContext uint64 // SSL context pointer (session identifier)

	// Response
	ResponseHeaders map[string]string
	Code            int
	IsChunked       bool
	ResponsePayload []byte
}

func (e *HttpResponseEvent) Type() EventType { return e.EventType }

// SSEEvent represents Server-Sent Events received through an HTTP connection
// Will create EventTypeHttpSSE
type SSEEvent struct {
	EventHeader
	HttpRequestEvent

	SSLContext uint64 // SSL context pointer (session identifier)

	// SSE event type (e.g., "message", "update", etc.)
	SSEEventType string
	// SSE data
	Data []byte
}

func (e *SSEEvent) Type() EventType { return e.EventType }
