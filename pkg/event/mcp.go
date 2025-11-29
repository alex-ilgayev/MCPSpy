package event

import (
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

// Copy creates a deep copy of the MCPEvent to avoid data races in async processing
func (e *MCPEvent) Copy() *MCPEvent {
	cp := &MCPEvent{
		Timestamp:     e.Timestamp,
		TransportType: e.TransportType,
		Raw:           e.Raw,
		JSONRPCMessage: JSONRPCMessage{
			MessageType: e.MessageType,
			ID:          e.ID,
			Method:      e.Method,
			Result:      e.Result,
			Error:       e.Error,
		},
	}

	// Deep copy Params map
	if e.Params != nil {
		cp.Params = make(map[string]interface{}, len(e.Params))
		for k, v := range e.Params {
			cp.Params[k] = v
		}
	}

	// Deep copy Request if present
	if e.Request != nil {
		cp.Request = &JSONRPCMessage{
			MessageType: e.Request.MessageType,
			ID:          e.Request.ID,
			Method:      e.Request.Method,
			Result:      e.Request.Result,
			Error:       e.Request.Error,
		}
		if e.Request.Params != nil {
			cp.Request.Params = make(map[string]interface{}, len(e.Request.Params))
			for k, v := range e.Request.Params {
				cp.Request.Params[k] = v
			}
		}
	}

	// Deep copy transport info
	if e.StdioTransport != nil {
		cp.StdioTransport = &StdioTransport{
			FromPID:  e.StdioTransport.FromPID,
			FromComm: e.StdioTransport.FromComm,
			ToPID:    e.StdioTransport.ToPID,
			ToComm:   e.StdioTransport.ToComm,
		}
	}
	if e.HttpTransport != nil {
		cp.HttpTransport = &HttpTransport{
			PID:       e.HttpTransport.PID,
			Comm:      e.HttpTransport.Comm,
			Host:      e.HttpTransport.Host,
			IsRequest: e.HttpTransport.IsRequest,
		}
	}

	return cp
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
