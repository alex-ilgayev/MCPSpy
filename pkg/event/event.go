package event

import (
	"github.com/alex-ilgayev/mcpspy/pkg/encoder"
	"github.com/sirupsen/logrus"
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
	// Complete JSON message aggregated from raw FS read events
	EventTypeFSAggregatedRead EventType = 104
	// Complete JSON message aggregated from raw FS write events
	EventTypeFSAggregatedWrite EventType = 105
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
	case EventTypeMCPMessage:
		return "mcp_message"
	case EventTypeFSAggregatedRead:
		return "fs_aggregated_read"
	case EventTypeFSAggregatedWrite:
		return "fs_aggregated_write"
	default:
		return "unknown"
	}
}

// Event is the interface for all events
type Event interface {
	Type() EventType

	// Helper for logging
	LogFields() logrus.Fields
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

// FSEventBase contains common fields for all filesystem events
type FSEventBase struct {
	EventHeader

	Inode    uint32    // Inode number for correlation
	FromPID  uint32    // Sender (writer) PID
	FromComm [16]uint8 // Sender comm
	ToPID    uint32    // Receiver (reader) PID
	ToComm   [16]uint8 // Receiver comm
	_        [4]uint8  // Explicit padding for 8-byte alignment of FilePtr
	FilePtr  uint64    // File pointer (struct file*) for session tracking
}

func (e *FSEventBase) FromCommStr() string {
	return encoder.BytesToStr(e.FromComm[:])
}

func (e *FSEventBase) ToCommStr() string {
	return encoder.BytesToStr(e.ToComm[:])
}

// FSDataEvent represents raw r/w payload events from eBPF
type FSDataEvent struct {
	FSEventBase

	Size    uint32            // Actual data size
	BufSize uint32            // Size of data in buf (may be truncated)
	Buf     [128 * 1024]uint8 // Data buffer
}

func (e *FSDataEvent) Type() EventType { return e.EventType }
func (e *FSDataEvent) Buffer() []byte {
	return e.Buf[:e.BufSize]
}
func (e *FSDataEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":      e.PID,
		"comm":     e.Comm(),
		"size":     e.Size,
		"buf_size": e.BufSize,
	}
}

// FSAggregatedEvent represents a complete JSON message aggregated from
// multiple raw FS events in userspace
type FSAggregatedEvent struct {
	FSEventBase

	Payload []byte // Complete JSON message
}

func (e *FSAggregatedEvent) Type() EventType { return e.EventType }
func (e *FSAggregatedEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":  e.PID,
		"comm": e.Comm(),
		"size": len(e.Payload),
	}
}

// NewFSAggregatedEvent creates a new FSAggregatedEvent for usermode-aggregated JSON
func NewFSAggregatedEvent(
	eventType EventType,
	pid uint32,
	comm [16]uint8,
	inode uint32,
	fromPID uint32,
	fromComm [16]uint8,
	toPID uint32,
	toComm [16]uint8,
	filePtr uint64,
	payload []byte,
) *FSAggregatedEvent {
	return &FSAggregatedEvent{
		FSEventBase: FSEventBase{
			EventHeader: EventHeader{
				EventType: eventType,
				PID:       pid,
				CommBytes: comm,
			},
			Inode:    inode,
			FromPID:  fromPID,
			FromComm: fromComm,
			ToPID:    toPID,
			ToComm:   toComm,
			FilePtr:  filePtr,
		},
		Payload: payload,
	}
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
func (e *LibraryEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":      e.PID,
		"comm":     e.Comm(),
		"inode":    e.Inode,
		"path":     e.Path(),
		"mnt_nsid": e.MntNSID,
	}
}

// Even though it's similar to DataEvent,
// we need to treat it differently, as it consist
// of HTTP data, and not neccesarily MCP data.
type TlsPayloadEvent struct {
	EventHeader

	SSLContext  uint64            // SSL context pointer (session identifier)
	Size        uint32            // Actual data size
	BufSize     uint32            // Size of data in buf (may be truncated)
	HttpVersion HttpVersion       // Identified HTTP version
	Buf         [128 * 1024]uint8 // Data buffer
}

func (e *TlsPayloadEvent) Type() EventType { return e.EventType }
func (e *TlsPayloadEvent) Buffer() []byte {
	return e.Buf[:e.BufSize]
}
func (e *TlsPayloadEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":      e.PID,
		"comm":     e.Comm(),
		"ssl_ctx":  e.SSLContext,
		"size":     e.Size,
		"buf_size": e.BufSize,
		"version":  e.HttpVersion.String(),
	}
}

type TlsFreeEvent struct {
	EventHeader

	SSLContext uint64
}

func (e *TlsFreeEvent) Type() EventType { return e.EventType }
func (e *TlsFreeEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":     e.PID,
		"comm":    e.Comm(),
		"ssl_ctx": e.SSLContext,
	}
}

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
func (e *HttpRequestEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":     e.PID,
		"comm":    e.Comm(),
		"ssl_ctx": e.SSLContext,
		"method":  e.Method,
		"host":    e.Host,
		"path":    e.Path,
	}
}

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
func (e *HttpResponseEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":        e.PID,
		"comm":       e.Comm(),
		"ssl_ctx":    e.SSLContext,
		"method":     e.Method,
		"host":       e.Host,
		"path":       e.Path,
		"code":       e.Code,
		"is_chunked": e.IsChunked,
	}
}

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
func (e *SSEEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"pid":       e.PID,
		"comm":      e.Comm(),
		"ssl_ctx":   e.SSLContext,
		"method":    e.Method,
		"host":      e.Host,
		"path":      e.Path,
		"sse_event": e.SSEEventType,
	}
}
