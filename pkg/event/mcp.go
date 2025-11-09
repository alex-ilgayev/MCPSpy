package event

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// JSONRPCMessageType represents the type of JSON-RPC message
type JSONRPCMessageType string

const (
	JSONRPCMessageTypeRequest      JSONRPCMessageType = "request"
	JSONRPCMessageTypeResponse     JSONRPCMessageType = "response"
	JSONRPCMessageTypeNotification JSONRPCMessageType = "notification"
)

// TransportType represents the type of transport
type TransportType string

const (
	TransportTypeStdio TransportType = "stdio"
	TransportTypeSSE   TransportType = "sse"
	TransportTypeHTTP  TransportType = "http"
)

// ProcessHop represents a single hop in the message flow through processes.
// Each hop captures who sent the message and who received it at this stage.
type ProcessHop struct {
	FromPID   uint32    `json:"from_pid"`
	FromComm  string    `json:"from_comm"`
	ToPID     uint32    `json:"to_pid"`
	ToComm    string    `json:"to_comm"`
	Timestamp time.Time `json:"timestamp"`
}

// ProcessChain represents the complete chain of processes a message has traveled through.
// For example, if a message goes: Client (PID 100) -> Proxy (PID 200) -> Server (PID 300)
// The chain would contain two hops: [100->200, 200->300]
type ProcessChain struct {
	Hops []ProcessHop `json:"hops,omitempty"`
}

// Signature returns a unique string representation of the process chain.
// This is used for correlation and deduplication.
// Format: "FromPID1->ToPID1|FromPID2->ToPID2|..."
func (pc *ProcessChain) Signature() string {
	if pc == nil || len(pc.Hops) == 0 {
		return ""
	}

	var sig string
	for i, hop := range pc.Hops {
		if i > 0 {
			sig += "|"
		}
		sig += fmt.Sprintf("%d->%d", hop.FromPID, hop.ToPID)
	}
	return sig
}

// CorrelationSignature returns a normalized signature for request/response correlation.
// For STDIO transport, requests and responses between the same processes flow in opposite
// directions (A->B for request, B->A for response), so we normalize by sorting PIDs.
// Format: "PID1<->PID2" where PID1 < PID2
func (pc *ProcessChain) CorrelationSignature() string {
	if pc == nil || len(pc.Hops) == 0 {
		return ""
	}

	// For simplicity, use the first hop for correlation
	// Normalize by sorting the PIDs to make it direction-independent
	hop := pc.Hops[0]
	pid1, pid2 := hop.FromPID, hop.ToPID

	if pid1 > pid2 {
		pid1, pid2 = pid2, pid1
	}

	return fmt.Sprintf("%d<->%d", pid1, pid2)
}

// SourcePID returns the PID of the original sender (first FromPID in the chain)
func (pc *ProcessChain) SourcePID() uint32 {
	if pc == nil || len(pc.Hops) == 0 {
		return 0
	}
	return pc.Hops[0].FromPID
}

// DestPID returns the PID of the final receiver (last ToPID in the chain)
func (pc *ProcessChain) DestPID() uint32 {
	if pc == nil || len(pc.Hops) == 0 {
		return 0
	}
	return pc.Hops[len(pc.Hops)-1].ToPID
}

// AddHop adds a new hop to the process chain if it's not already present
func (pc *ProcessChain) AddHop(hop ProcessHop) bool {
	if pc == nil {
		return false
	}

	// Check if this exact hop already exists
	for _, existing := range pc.Hops {
		if existing.FromPID == hop.FromPID && existing.ToPID == hop.ToPID {
			return false // Already exists
		}
	}

	pc.Hops = append(pc.Hops, hop)
	return true
}

// StdioTransport represents the info relevant for the stdio transport.
type StdioTransport struct {
	FromPID  uint32 `json:"from_pid"`
	FromComm string `json:"from_comm"`
	ToPID    uint32 `json:"to_pid"`
	ToComm   string `json:"to_comm"`
}

type HttpTransport struct {
	PID       uint32 `json:"pid,omitempty"`
	Comm      string `json:"comm,omitempty"`
	Host      string `json:"host,omitempty"`
	IsRequest bool   `json:"is_request,omitempty"`
}

// JSONRPCMessage represents a parsed JSON-RPC 2.0 message.
type JSONRPCMessage struct {
	MessageType JSONRPCMessageType     `json:"type"`
	ID          interface{}            `json:"id,omitempty"` // string or number
	Method      string                 `json:"method,omitempty"`
	Params      map[string]interface{} `json:"params,omitempty"`
	Result      interface{}            `json:"result,omitempty"`
	Error       JSONRPCError           `json:"error,omitempty"`

	// Request holds the original request message for response messages.
	// This field is nil for request and notification messages.
	// For response messages, it contains the corresponding request that triggered this response.
	Request *JSONRPCMessage `json:"request,omitempty"`
}

func (m *JSONRPCMessage) LogFields() logrus.Fields {
	fields := logrus.Fields{
		"msg_type": m.MessageType,
		"id":       m.ID,
		"method":   m.Method,
	}

	// Include error information if present
	if m.Error.Code != 0 || m.Error.Message != "" {
		fields["error_code"] = m.Error.Code
		fields["error"] = m.Error.Message
	}

	return fields
}

// JSONRPCError represents a JSON-RPC error
type JSONRPCError struct {
	Code    int         `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// MCPEvent represents a parsed MCP message
type MCPEvent struct {
	Timestamp       time.Time     `json:"timestamp"`
	TransportType   TransportType `json:"transport_type"`
	*StdioTransport `json:"stdio_transport,omitempty"`
	*HttpTransport  `json:"http_transport,omitempty"`

	// ProcessChain tracks all process hops this message has traveled through.
	// This is particularly useful for Docker-based MCP servers where messages
	// flow through intermediate processes (e.g., Client -> Docker proxy -> Server).
	ProcessChain *ProcessChain `json:"process_chain,omitempty"`

	JSONRPCMessage

	Raw string `json:"raw"`
}

func (e *MCPEvent) Type() EventType { return EventTypeMCPMessage }
func (e *MCPEvent) LogFields() logrus.Fields {
	fields := e.JSONRPCMessage.LogFields()
	fields["transport"] = e.TransportType

	if e.StdioTransport != nil {
		fields["from_pid"] = e.StdioTransport.FromPID
		fields["from_comm"] = e.StdioTransport.FromComm
		fields["to_pid"] = e.StdioTransport.ToPID
		fields["to_comm"] = e.StdioTransport.ToComm
	}
	if e.HttpTransport != nil {
		fields["pid"] = e.HttpTransport.PID
		fields["comm"] = e.HttpTransport.Comm
		fields["host"] = e.HttpTransport.Host
		fields["is_request"] = e.HttpTransport.IsRequest
	}

	// Add process chain information if available
	if e.ProcessChain != nil && len(e.ProcessChain.Hops) > 0 {
		fields["process_chain"] = e.ProcessChain.Signature()
		fields["source_pid"] = e.ProcessChain.SourcePID()
		fields["dest_pid"] = e.ProcessChain.DestPID()
		fields["chain_length"] = len(e.ProcessChain.Hops)
	}

	return fields
}

// ExtractToolName attempts to extract tool name from a tools/call request
func (msg *MCPEvent) ExtractToolName() string {
	if msg.Method != "tools/call" || msg.Params == nil {
		return ""
	}

	if name, ok := msg.Params["name"].(string); ok {
		return name
	}

	return ""
}

// ExtractResourceURI attempts to extract resource URI from resource-related requests
func (msg *MCPEvent) ExtractResourceURI() string {
	// Check if this is a resource method that has a URI parameter
	if (msg.Method != "resources/read" &&
		msg.Method != "resources/subscribe" &&
		msg.Method != "resources/unsubscribe") ||
		msg.Params == nil {
		return ""
	}

	if uri, ok := msg.Params["uri"].(string); ok {
		return uri
	}

	return ""
}
