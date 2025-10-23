package mcp

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

var (
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

// Parser handles parsing of MCP messages
// Subscribes to the following events:
// - EventTypeFSAggregatedRead
// - EventTypeFSAggregatedWrite
// - EventTypeHttpRequest
// - EventTypeHttpResponse
// - EventTypeHttpSSE
//
// Emits the following events:
// - EventTypeMCPMessage
type Parser struct {
	// Cache for correlating requests and responses by ID.
	// Stores request IDs to validate that responses correspond to actual requests.
	// Thread-safe.
	requestIDCache *expirable.LRU[string, struct{}]

	// Cache for detecting duplicate messages.
	// Once we see a hash, we don't emit it again (first one wins).
	// Relevant for docker-based MCPs which may emit duplicates.
	// Thread-safe.
	seenHashCache *expirable.LRU[string, struct{}]

	eventBus bus.EventBus
}

// NewParser creates a new MCP parser
func NewParser(eventBus bus.EventBus) (*Parser, error) {
	p := &Parser{
		requestIDCache: expirable.NewLRU[string, struct{}](requestIDCacheSize, nil, requestIDCacheTTL),
		seenHashCache:  expirable.NewLRU[string, struct{}](seenHashCacheSize, nil, seenHashCacheTTL),
		eventBus:       eventBus,
	}

	if err := p.eventBus.Subscribe(event.EventTypeFSAggregatedRead, p.ParseDataStdio); err != nil {
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeFSAggregatedWrite, p.ParseDataStdio); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpRequest, p.ParseDataHttp); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpResponse, p.ParseDataHttp); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpSSE, p.ParseDataHttp); err != nil {
		p.Close()
		return nil, err
	}

	return p, nil
}

// ParseDataStdio attempts to parse MCP messages from aggregated Stdio data.
// The parsing flow is split into several parts:
// 1. Duplicate detection (drop duplicates, first one wins)
// 2. JSON-RPC parsing
// 3. MCP validation
// 4. Request/response correlation (by JSON-RPC ID)
//
// Note: Write/read correlation is done in kernel-mode via inode tracking
// and JSON aggregation is done in userspace by the FS session manager.
// The events passed here are complete JSON messages ready for parsing.
func (p *Parser) ParseDataStdio(e event.Event) {
	stdioEvent, ok := e.(*event.FSAggregatedEvent)
	if !ok {
		return
	}

	buf := stdioEvent.Payload
	if len(buf) == 0 {
		return
	}

	logrus.WithFields(e.LogFields()).Trace("Parsing STDIO data for MCP")

	// Use JSON decoder to handle multi-line JSON properly
	decoder := json.NewDecoder(bytes.NewReader(buf))
	for {
		var jsonData json.RawMessage
		if err := decoder.Decode(&jsonData); err != nil {
			if err == io.EOF {
				break
			}
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to decode JSON")
			return
		}

		if len(bytes.TrimSpace(jsonData)) == 0 {
			continue
		}

		// Part 1: Duplicate detection
		hash := p.calculateHash(jsonData)
		if p.isDuplicate(hash) {
			continue // Skip duplicates, first one wins
		}

		// Part 2 & 3: Parse JSON-RPC and validate MCP
		jsonRpcMsg, err := p.parseJSONRPC(jsonData)
		if err != nil {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to parse JSON-RPC")
			return
		}

		if ok, err := p.validateMCPMessage(jsonRpcMsg); !ok {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Invalid MCP message")
			return
		}

		// Part 4: Handle request/response correlation
		if !p.handleRequestResponseCorrelation(jsonRpcMsg) {
			// Drop responses without matching request IDs
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				Debug("Dropping response without matching request ID")
			continue
		}

		// Create message with kernel-provided correlation
		msg := &event.MCPEvent{
			Timestamp:     time.Now(),
			Raw:           string(jsonData),
			TransportType: event.TransportTypeStdio,
			StdioTransport: &event.StdioTransport{
				FromPID:  stdioEvent.FromPID,
				FromComm: stdioEvent.FromCommStr(),
				ToPID:    stdioEvent.ToPID,
				ToComm:   stdioEvent.ToCommStr(),
			},
			JSONRPCMessage: jsonRpcMsg,
		}

		logrus.WithFields(msg.LogFields()).Trace(fmt.Sprintf("event#%s", msg.Type().String()))

		p.eventBus.Publish(msg)
	}
}

// ParseDataHttp attempts to parse MCP messages from HTTP payload data
// This method is used for HTTP transport where MCP messages are sent via HTTP requests/responses
// func (p *Parser) ParseDataHttp(data []byte, eventType event.EventType, pid uint32, comm string, host string, isRequest bool) ([]*event.MCPEvent, error) {
func (p *Parser) ParseDataHttp(e event.Event) {
	// Extract relevant fields from the event
	var buf []byte
	var pid uint32
	var comm string
	var host string
	var isRequest bool

	switch event := e.(type) {
	case *event.HttpRequestEvent:
		buf = event.RequestPayload
		pid = event.PID
		comm = event.Comm()
		host = event.Host
		isRequest = true
	case *event.HttpResponseEvent:
		buf = event.ResponsePayload
		pid = event.PID
		comm = event.Comm()
		host = event.Host
		isRequest = false
	case *event.SSEEvent:
		buf = event.Data
		pid = event.PID
		comm = event.Comm()
		host = event.Host
		isRequest = false
	default:
		return
	}

	logrus.WithFields(e.LogFields()).Trace("Parsing HTTP data for MCP")

	// Use JSON decoder to handle multi-line JSON properly
	decoder := json.NewDecoder(bytes.NewReader(buf))
	for {
		var jsonData json.RawMessage
		if err := decoder.Decode(&jsonData); err != nil {
			if err == io.EOF {
				break
			}
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to decode JSON")
			return
		}

		if len(bytes.TrimSpace(jsonData)) == 0 {
			continue
		}

		// Parse the message
		jsonRpcMsg, err := p.parseJSONRPC(jsonData)
		if err != nil {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to parse JSON-RPC")
			return
		}

		if ok, err := p.validateMCPMessage(jsonRpcMsg); !ok {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Invalid MCP message")
			return
		}

		// Handle request/response correlation
		if !p.handleRequestResponseCorrelation(jsonRpcMsg) {
			// Drop responses without matching request IDs
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				Debug("Dropping response without matching request ID")
			continue
		}

		// Create http transport info from correlated events
		msg := &event.MCPEvent{
			Timestamp:     time.Now(),
			Raw:           string(jsonData),
			TransportType: event.TransportTypeHTTP,
			HttpTransport: &event.HttpTransport{
				PID:       pid,
				Comm:      comm,
				Host:      host,
				IsRequest: isRequest,
			},
			JSONRPCMessage: jsonRpcMsg,
		}

		logrus.WithFields(msg.LogFields()).Trace(fmt.Sprintf("event#%s", msg.Type().String()))

		p.eventBus.Publish(msg)
	}
}

// parseJSONRPC parses a single JSON-RPC message
func (p *Parser) parseJSONRPC(data []byte) (event.JSONRPCMessage, error) {
	// Validate JSON
	if !gjson.ValidBytes(data) {
		return event.JSONRPCMessage{}, fmt.Errorf("invalid JSON")
	}

	result := gjson.ParseBytes(data)

	// Check for jsonrpc field
	if result.Get("jsonrpc").String() != "2.0" {
		return event.JSONRPCMessage{}, fmt.Errorf("not JSON-RPC 2.0")
	}

	msg := event.JSONRPCMessage{}

	// Determine message type
	// Requirements for Request type: method and id
	// Requirements for Response type: id and either result or error
	// Requirements for Notification type: method and id is missing
	if result.Get("method").Exists() && result.Get("id").Exists() {
		msg.MessageType = event.JSONRPCMessageTypeRequest
		msg.ID = parseID(result.Get("id"))
		msg.Method = result.Get("method").String()

		// Parse params if present
		if params := result.Get("params"); params.Exists() {
			msg.Params = parseParams(params)
		}
	} else if result.Get("id").Exists() && (result.Get("result").Exists() || result.Get("error").Exists()) {
		msg.MessageType = event.JSONRPCMessageTypeResponse
		msg.ID = parseID(result.Get("id"))

		if result.Get("result").Exists() {
			msg.Result = result.Get("result").Value()
		}

		if errResult := result.Get("error"); errResult.Exists() {
			msg.Error = event.JSONRPCError{
				Code:    int(errResult.Get("code").Int()),
				Message: errResult.Get("message").String(),
				Data:    errResult.Get("data").Value(),
			}
		}
	} else if result.Get("method").Exists() {
		msg.MessageType = event.JSONRPCMessageTypeNotification
		msg.Method = result.Get("method").String()

		// Parse params if present
		if params := result.Get("params"); params.Exists() {
			msg.Params = parseParams(params)
		}
	} else {
		return event.JSONRPCMessage{}, fmt.Errorf("unknown JSON-RPC message type")
	}

	return msg, nil
}

// validateMCPMessage validates that the message is a valid MCP message.
// Currently, we only validate the method.
// TODO: Validate that responses are valid (with matching id for requests).
func (p *Parser) validateMCPMessage(msg event.JSONRPCMessage) (bool, error) {
	switch msg.MessageType {
	case event.JSONRPCMessageTypeRequest:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return false, fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID == nil {
			return false, fmt.Errorf("request message has no id")
		}

		return true, nil
	case event.JSONRPCMessageTypeResponse:
		if msg.ID == nil {
			return false, fmt.Errorf("response message has no id")
		}

		return true, nil
	case event.JSONRPCMessageTypeNotification:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return false, fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID != nil {
			return false, fmt.Errorf("notification message has id")
		}

		return true, nil
	}

	return false, fmt.Errorf("unknown JSON-RPC message type: %s", msg.MessageType)
}

// calculateHash creates a hash of the buffer content for duplicate detection
func (p *Parser) calculateHash(buf []byte) string {
	hash := sha1.Sum(buf)
	return fmt.Sprintf("%x", hash)
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
func (p *Parser) handleRequestResponseCorrelation(msg event.JSONRPCMessage) bool {
	switch msg.MessageType {
	case event.JSONRPCMessageTypeRequest:
		// Cache request ID for future response validation
		p.cacheRequestID(msg.ID)
		return true
	case event.JSONRPCMessageTypeResponse:
		// Validate that this response corresponds to a known request
		return p.validateResponseID(msg.ID)
	}
	// Notifications don't have IDs, always keep them
	return true
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

func (p *Parser) Close() {
	p.eventBus.Unsubscribe(event.EventTypeFSAggregatedRead, p.ParseDataStdio)
	p.eventBus.Unsubscribe(event.EventTypeFSAggregatedWrite, p.ParseDataStdio)
	p.eventBus.Unsubscribe(event.EventTypeHttpRequest, p.ParseDataHttp)
	p.eventBus.Unsubscribe(event.EventTypeHttpResponse, p.ParseDataHttp)
	p.eventBus.Unsubscribe(event.EventTypeHttpSSE, p.ParseDataHttp)
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
