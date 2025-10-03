package mcp

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

var (
	writeCacheSize     = 4096
	writeCacheTTL      = 2 * time.Second
	requestIDCacheSize = 4096
	requestIDCacheTTL  = 5 * time.Second
	seenHashCacheSize  = 4096
	seenHashCacheTTL   = 2 * time.Second
)

// Protocol resources:
// - Spec: https://modelcontextprotocol.io/specification/2025-06-18
// - Schema: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-06-18/schema.ts

// List of allowed methods according to the schema,
// and their descriptions.
var allowedMCPMethods = map[string]string{
	// Lifecycle
	"initialize":                "Initialize connection",
	"ping":                      "Ping connection",
	"notifications/initialized": "Connection initialized",
	"notifications/cancelled":   "Connection cancelled",

	// Tools
	"tools/list":                       "List available tools",
	"tools/call":                       "Execute a tool",
	"notifications/tools/list_changed": "Tool list changed",

	// Resources
	"resources/list":                       "List available resources",
	"resources/templates/list":             "List available resource templates",
	"resources/read":                       "Read a resource",
	"resources/subscribe":                  "Subscribe to resource updates",
	"resources/unsubscribe":                "Unsubscribe from resource updates",
	"notifications/resources/list_changed": "Resource list changed",
	"notifications/resources/updated":      "Resource updated",

	// Prompts
	"prompts/list":                       "List available prompts",
	"prompts/get":                        "Get a prompt",
	"completion/complete":                "Complete a prompt",
	"notifications/prompts/list_changed": "Prompt list changed",

	// Notifications
	"notifications/progress": "Progress update",

	// Logging
	"logging/setLevel":      "Set logging level",
	"notifications/message": "Log message",

	// Client capabilities
	"sampling/createMessage":           "Create LLM message",
	"elicitation/create":               "Create elicitation",
	"roots/list":                       "List roots",
	"notifications/roots/list_changed": "Root list changed",
}

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
	Type   JSONRPCMessageType     `json:"type"`
	ID     interface{}            `json:"id,omitempty"` // string or number
	Method string                 `json:"method,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
	Result interface{}            `json:"result,omitempty"`
	Error  JSONRPCError           `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error
type JSONRPCError struct {
	Code    int         `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// Message represents a parsed MCP message
type Message struct {
	Timestamp       time.Time     `json:"timestamp"`
	TransportType   TransportType `json:"transport_type"`
	*StdioTransport `json:"stdio_transport,omitempty"`
	*HttpTransport  `json:"http_transport,omitempty"`

	JSONRPCMessage

	Raw string `json:"raw"`
}

// ExtractToolName attempts to extract tool name from a tools/call request
func (msg *Message) ExtractToolName() string {
	if msg.Method != "tools/call" || msg.Params == nil {
		return ""
	}

	if name, ok := msg.Params["name"].(string); ok {
		return name
	}

	return ""
}

// ExtractResourceURI attempts to extract resource URI from resource-related requests
func (msg *Message) ExtractResourceURI() string {
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

// WriteEvent represents a cached write event
// We do not store the buffer, because it should be the same as the subsequent read event.
// The hash function is constant and hardcoded.
type WriteEvent struct {
	PID  uint32
	Comm string
}

// Parser handles parsing of MCP messages
type Parser struct {
	// Cache for correlating write and read events.
	// Using an LRU which has expiration.
	// Thread-safe.
	writeCache *expirable.LRU[string, WriteEvent]

	// Cache for correlating requests and responses by ID.
	// Stores request IDs to validate that responses correspond to actual requests.
	// Thread-safe.
	requestIDCache *expirable.LRU[string, struct{}]

	// Cache for detecting duplicate messages.
	// Once we see a hash, we don't emit it again (first one wins).
	// Relevant for docker-based MCPs which may emit duplicates.
	// Thread-safe.
	seenHashCache *expirable.LRU[string, struct{}]
}

// NewParser creates a new MCP parser
func NewParser() *Parser {
	return &Parser{
		writeCache:     expirable.NewLRU[string, WriteEvent](writeCacheSize, nil, writeCacheTTL),
		requestIDCache: expirable.NewLRU[string, struct{}](requestIDCacheSize, nil, requestIDCacheTTL),
		seenHashCache:  expirable.NewLRU[string, struct{}](seenHashCacheSize, nil, seenHashCacheTTL),
	}
}

// ParseDataStdio attempts to parse MCP messages from Stdio raw data.
// The parsing flow is split into several parts:
// 1. Write/read correlation (by hash)
// 2. Duplicate detection (drop duplicates, first one wins)
// 3. JSON-RPC parsing
// 4. MCP validation
// 5. Request/response correlation (by JSON-RPC ID)
func (p *Parser) ParseDataStdio(data []byte, eventType event.EventType, pid uint32, comm string) ([]*Message, error) {
	if eventType != event.EventTypeFSWrite && eventType != event.EventTypeFSRead {
		return []*Message{}, fmt.Errorf("unknown event type in stdio parsing: %d", eventType)
	}

	var messages []*Message

	// Use JSON decoder to handle multi-line JSON properly
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var jsonData json.RawMessage
		if err := decoder.Decode(&jsonData); err != nil {
			if err == io.EOF {
				break
			}
			return []*Message{}, fmt.Errorf("failed to decode JSON: %w", err)
		}

		if len(bytes.TrimSpace(jsonData)) == 0 {
			continue
		}

		// Part 1: Write/read correlation
		if eventType == event.EventTypeFSWrite {
			p.cacheWriteEvent(jsonData, pid, comm)
			continue
		}

		// Read event - correlate with write
		writeEvent, hash, err := p.correlateReadWithWrite(jsonData)
		if err != nil {
			return []*Message{}, err
		}

		// Part 2: Duplicate detection
		if p.isDuplicate(hash) {
			continue // Skip duplicates, first one wins
		}

		// Part 3 & 4: Parse JSON-RPC and validate MCP
		msg, err := p.parseAndValidateMessage(jsonData, writeEvent, pid, comm)
		if err != nil {
			return nil, err
		}

		// Handle request/response correlation
		if !p.handleRequestResponseCorrelation(msg.JSONRPCMessage) {
			logrus.WithFields(logrus.Fields{
				"pid":    pid,
				"comm":   comm,
				"method": msg.Method,
				"id":     msg.ID,
			}).Debug("Dropping response without matching request ID")
			continue
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

// ParseDataHttp attempts to parse MCP messages from HTTP payload data
// This method is used for HTTP transport where MCP messages are sent via HTTP requests/responses
func (p *Parser) ParseDataHttp(data []byte, eventType event.EventType, pid uint32, comm string, host string, isRequest bool) ([]*Message, error) {
	var messages []*Message

	if eventType != event.EventTypeHttpRequest && eventType != event.EventTypeHttpResponse && eventType != event.EventTypeHttpSSE {
		return []*Message{}, fmt.Errorf("unknown event type in http parsing: %d", eventType)
	}

	// Split the data into individual JSON messages
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		msgData := scanner.Bytes()
		if len(bytes.TrimSpace(msgData)) == 0 {
			continue
		}

		// Parse the message
		jsonRpcMsg, err := p.parseJSONRPC(msgData)
		if err != nil {
			return nil, fmt.Errorf("failed to parse JSON-RPC: %w", err)
		}

		if ok, err := p.validateMCPMessage(jsonRpcMsg); !ok {
			return nil, fmt.Errorf("invalid MCP message: %w", err)
		}

		// Handle request/response correlation
		if !p.handleRequestResponseCorrelation(jsonRpcMsg) {
			// Drop responses without matching request IDs
			logrus.WithFields(logrus.Fields{
				"pid":    pid,
				"comm":   comm,
				"host":   host,
				"method": jsonRpcMsg.Method,
				"id":     jsonRpcMsg.ID,
			}).Debug("Dropping response without matching request ID")
			continue
		}

		// Create http transport info from correlated events
		messages = append(messages, &Message{
			Timestamp:     time.Now(),
			Raw:           string(msgData),
			TransportType: TransportTypeHTTP,
			HttpTransport: &HttpTransport{
				PID:       pid,
				Comm:      comm,
				Host:      host,
				IsRequest: isRequest,
			},
			JSONRPCMessage: jsonRpcMsg,
		})
	}

	return messages, nil
}

// parseJSONRPC parses a single JSON-RPC message
func (p *Parser) parseJSONRPC(data []byte) (JSONRPCMessage, error) {
	// Validate JSON
	if !gjson.ValidBytes(data) {
		return JSONRPCMessage{}, fmt.Errorf("invalid JSON")
	}

	result := gjson.ParseBytes(data)

	// Check for jsonrpc field
	if result.Get("jsonrpc").String() != "2.0" {
		return JSONRPCMessage{}, fmt.Errorf("not JSON-RPC 2.0")
	}

	msg := JSONRPCMessage{}

	// Determine message type
	// Requirements for Request type: method and id
	// Requirements for Response type: id and either result or error
	// Requirements for Notification type: method and id is missing
	if result.Get("method").Exists() && result.Get("id").Exists() {
		msg.Type = JSONRPCMessageTypeRequest
		msg.ID = parseID(result.Get("id"))
		msg.Method = result.Get("method").String()

		// Parse params if present
		if params := result.Get("params"); params.Exists() {
			msg.Params = parseParams(params)
		}
	} else if result.Get("id").Exists() && (result.Get("result").Exists() || result.Get("error").Exists()) {
		msg.Type = JSONRPCMessageTypeResponse
		msg.ID = parseID(result.Get("id"))

		if result.Get("result").Exists() {
			msg.Result = result.Get("result").Value()
		}

		if errResult := result.Get("error"); errResult.Exists() {
			msg.Error = JSONRPCError{
				Code:    int(errResult.Get("code").Int()),
				Message: errResult.Get("message").String(),
				Data:    errResult.Get("data").Value(),
			}
		}
	} else if result.Get("method").Exists() {
		msg.Type = JSONRPCMessageTypeNotification
		msg.Method = result.Get("method").String()

		// Parse params if present
		if params := result.Get("params"); params.Exists() {
			msg.Params = parseParams(params)
		}
	} else {
		return JSONRPCMessage{}, fmt.Errorf("unknown JSON-RPC message type")
	}

	return msg, nil
}

// validateMCPMessage validates that the message is a valid MCP message.
// Currently, we only validate the method.
// TODO: Validate that responses are valid (with matching id for requests).
func (p *Parser) validateMCPMessage(msg JSONRPCMessage) (bool, error) {
	switch msg.Type {
	case JSONRPCMessageTypeRequest:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return false, fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID == nil {
			return false, fmt.Errorf("request message has no id")
		}

		return true, nil
	case JSONRPCMessageTypeResponse:
		if msg.ID == nil {
			return false, fmt.Errorf("response message has no id")
		}

		return true, nil
	case JSONRPCMessageTypeNotification:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return false, fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID != nil {
			return false, fmt.Errorf("notification message has id")
		}

		return true, nil
	}

	return false, fmt.Errorf("unknown JSON-RPC message type: %s", msg.Type)
}

// calculateHash creates a hash of the buffer content for matching
func (p *Parser) calculateHash(buf []byte) string {
	hash := sha1.Sum(buf)
	return fmt.Sprintf("%x", hash)
}

// cacheWriteEvent calcaulates the hash and
// caches a write event for further correlation with read event.
func (p *Parser) cacheWriteEvent(data []byte, pid uint32, comm string) {
	hashStr := p.calculateHash(data)
	p.writeCache.Add(hashStr, WriteEvent{
		PID:  pid,
		Comm: comm,
	})
}

// idToCacheKey converts a request/response ID to a cache key string
// According to parseID, ID can only be int64 or string
func (p *Parser) idToCacheKey(id interface{}) string {
	switch v := id.(type) {
	case int64:
		return fmt.Sprintf("i:%d", v)
	default:
		// String (or any other type treated as string)
		return fmt.Sprintf("s:%v", v)
	}
}

// cacheRequestID stores a request ID for future response correlation
func (p *Parser) cacheRequestID(id interface{}) {
	if id == nil {
		return
	}
	key := p.idToCacheKey(id)
	p.requestIDCache.Add(key, struct{}{})
}

// validateResponseID checks if a response ID has a corresponding cached request
func (p *Parser) validateResponseID(id interface{}) bool {
	if id == nil {
		return false
	}
	key := p.idToCacheKey(id)
	_, exists := p.requestIDCache.Get(key)
	return exists
}

// handleRequestResponseCorrelation handles caching request IDs and validating response IDs.
// Returns true if the message should be kept, false if it should be dropped.
func (p *Parser) handleRequestResponseCorrelation(msg JSONRPCMessage) bool {
	switch msg.Type {
	case JSONRPCMessageTypeRequest:
		// Cache request ID for future response validation
		p.cacheRequestID(msg.ID)
		return true
	case JSONRPCMessageTypeResponse:
		// Validate that this response corresponds to a known request
		return p.validateResponseID(msg.ID)
	}
	// Notifications don't have IDs, always keep them
	return true
}

// correlateReadWithWrite finds the matching write event for a read event.
// Returns the write event, hash, and error.
func (p *Parser) correlateReadWithWrite(jsonData []byte) (WriteEvent, string, error) {
	hash := p.calculateHash(jsonData)
	writeEvent, ok := p.writeCache.Get(hash)
	if !ok {
		return WriteEvent{}, "", fmt.Errorf("no write event found for the parsed read event")
	}
	return writeEvent, hash, nil
}

// isDuplicate checks if we've seen this hash before and marks it as seen.
// Returns true if it's a duplicate (already seen).
func (p *Parser) isDuplicate(hash string) bool {
	_, exists := p.seenHashCache.Get(hash)
	if exists {
		return true // Duplicate - we've seen this before
	}
	// Mark as seen
	p.seenHashCache.Add(hash, struct{}{})
	return false
}

// parseAndValidateMessage parses JSON-RPC and validates MCP message.
// Returns the complete Message ready to emit.
func (p *Parser) parseAndValidateMessage(jsonData []byte, writeEvent WriteEvent, readPID uint32, readComm string) (*Message, error) {
	// Parse JSON-RPC
	jsonRpcMsg, err := p.parseJSONRPC(jsonData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON-RPC: %w", err)
	}

	// Validate MCP
	if ok, err := p.validateMCPMessage(jsonRpcMsg); !ok {
		return nil, fmt.Errorf("invalid MCP message: %w", err)
	}

	// Create message
	msg := &Message{
		Timestamp:     time.Now(),
		Raw:           string(jsonData),
		TransportType: TransportTypeStdio,
		StdioTransport: &StdioTransport{
			FromPID:  writeEvent.PID,
			FromComm: writeEvent.Comm,
			ToPID:    readPID,
			ToComm:   readComm,
		},
		JSONRPCMessage: jsonRpcMsg,
	}

	return msg, nil
}

// parseID parses the ID field which can be string or number
func parseID(idResult gjson.Result) interface{} {
	if idResult.Type == gjson.Number {
		return idResult.Int()
	}
	return idResult.String()
}

// parseParams converts gjson result to map
func parseParams(params gjson.Result) map[string]interface{} {
	result := make(map[string]interface{})
	params.ForEach(func(key, value gjson.Result) bool {
		result[key.String()] = value.Value()
		return true
	})
	return result
}

// GetMethodDescription returns a human-readable description of the method
func GetMethodDescription(method string) string {
	if info, ok := allowedMCPMethods[method]; ok {
		return info
	}

	return "Unknown method"
}
