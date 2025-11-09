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

// messageMetadata holds metadata about a seen message including its process chain.
// This allows us to track all processes a message has traveled through,
// which is critical for Docker-based MCP servers with intermediate processes.
type messageMetadata struct {
	// contentHash is the SHA1 hash of the JSON content (used for detecting same logical message)
	contentHash string

	// processChain tracks all process hops this message has traveled through
	processChain *event.ProcessChain

	// firstSeen is when we first encountered this message
	firstSeen time.Time
}

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
	// Stores full request messages to enable pairing with their responses.
	// Thread-safe.
	requestIDCache *expirable.LRU[string, *event.JSONRPCMessage]

	// Cache for tracking seen messages with their process chains.
	// Instead of just dropping duplicates, we aggregate process hops
	// to build a complete picture of message flow through intermediate processes.
	// This is critical for Docker-based MCP servers.
	// Thread-safe.
	seenHashCache *expirable.LRU[string, *messageMetadata]

	eventBus bus.EventBus
}

// NewParser creates a new MCP parser
func NewParser(eventBus bus.EventBus) (*Parser, error) {
	p := &Parser{
		requestIDCache: expirable.NewLRU[string, *event.JSONRPCMessage](requestIDCacheSize, nil, requestIDCacheTTL),
		seenHashCache:  expirable.NewLRU[string, *messageMetadata](seenHashCacheSize, nil, seenHashCacheTTL),
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
// 1. Process chain tracking and aggregation (handles multi-hop messages)
// 2. JSON-RPC parsing
// 3. MCP validation
// 4. Request/response correlation (by JSON-RPC ID + process chain)
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

		// Part 1: Process chain tracking and aggregation
		// Create a process hop from this event
		hop := event.ProcessHop{
			FromPID:   stdioEvent.FromPID,
			FromComm:  stdioEvent.FromCommStr(),
			ToPID:     stdioEvent.ToPID,
			ToComm:    stdioEvent.ToCommStr(),
			Timestamp: time.Now(),
		}

		// Get or create metadata for this message
		hash := p.calculateHash(jsonData)
		metadata, isNew := p.getOrCreateMessageMetadata(hash, hop)

		// If this is a duplicate hop (we've seen this exact message before),
		// skip processing but the hop has been added to the chain
		if !isNew {
			logrus.WithFields(logrus.Fields{
				"hash":      hash,
				"from_pid":  hop.FromPID,
				"to_pid":    hop.ToPID,
				"chain_sig": metadata.processChain.Signature(),
			}).Trace("Skipping duplicate message (hop added to chain)")
			continue // Skip duplicates, first one wins for emission
		}

		// Part 2 & 3: Parse JSON-RPC and validate MCP
		jsonRpcMsg, err := p.parseJSONRPC(jsonData)
		if err != nil {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to parse JSON-RPC")
			return
		}

		if err := p.validateMCPMessage(jsonRpcMsg); err != nil {
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				WithError(err).
				Debug("Invalid MCP message")
			return
		}

		// Part 4: Handle request/response correlation with process chain (STDIO transport)
		if err := p.handleRequestResponseCorrelation(&jsonRpcMsg, metadata.processChain, false); err != nil {
			// Drop responses without matching request IDs
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				WithFields(logrus.Fields{
					"chain_sig": metadata.processChain.Signature(),
				}).
				Debug("Dropping response without matching request ID")
			return
		}

		// Create message with full process chain information
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
			ProcessChain:   metadata.processChain,
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

		// For HTTP transport, create a simple process hop
		// (HTTP typically doesn't have intermediate processes like Docker proxies)
		hop := event.ProcessHop{
			FromPID:   pid,
			FromComm:  comm,
			ToPID:     pid,
			ToComm:    comm,
			Timestamp: time.Now(),
		}

		// Get or create metadata for this message
		hash := p.calculateHash(jsonData)
		metadata, isNew := p.getOrCreateMessageMetadata(hash, hop)

		// Skip duplicates (though less common in HTTP transport)
		if !isNew {
			logrus.WithFields(logrus.Fields{
				"hash":      hash,
				"pid":       pid,
				"chain_sig": metadata.processChain.Signature(),
			}).Trace("Skipping duplicate HTTP message")
			continue
		}

		// Parse the message
		jsonRpcMsg, err := p.parseJSONRPC(jsonData)
		if err != nil {
			logrus.WithFields(e.LogFields()).WithError(err).Debug("Failed to parse JSON-RPC")
			return
		}

		if err := p.validateMCPMessage(jsonRpcMsg); err != nil {
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				WithError(err).Debug("Invalid MCP message")
			return
		}

		// Handle request/response correlation (HTTP doesn't use process chains for correlation)
		if err := p.handleRequestResponseCorrelation(&jsonRpcMsg, metadata.processChain, true); err != nil {
			// Drop responses without matching request IDs
			logrus.
				WithFields(e.LogFields()).
				WithFields(jsonRpcMsg.LogFields()).
				WithFields(logrus.Fields{
					"chain_sig": metadata.processChain.Signature(),
				}).
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
			ProcessChain:   metadata.processChain,
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
func (p *Parser) validateMCPMessage(msg event.JSONRPCMessage) error {
	switch msg.MessageType {
	case event.JSONRPCMessageTypeRequest:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID == nil {
			return fmt.Errorf("request message has no id")
		}

		return nil
	case event.JSONRPCMessageTypeResponse:
		if msg.ID == nil {
			return fmt.Errorf("response message has no id")
		}

		return nil
	case event.JSONRPCMessageTypeNotification:
		if _, ok := allowedMCPMethods[msg.Method]; !ok {
			return fmt.Errorf("unknown MCP method: %s", msg.Method)
		}

		if msg.ID != nil {
			return fmt.Errorf("notification message has id")
		}

		return nil
	}

	return fmt.Errorf("unknown JSON-RPC message type: %s", msg.MessageType)
}

// calculateHash creates a hash of the buffer content for duplicate detection
func (p *Parser) calculateHash(buf []byte) string {
	hash := sha1.Sum(buf)
	return fmt.Sprintf("%x", hash)
}

// idToCacheKey converts a request/response ID to a cache key string.
// The key now includes process chain information to prevent cross-process correlation issues.
// Format: "i:<id>|<corr_sig>" for integer IDs, "s:<id>|<corr_sig>" for string IDs
// where corr_sig is the normalized correlation signature (e.g., "100<->200")
// The correlation signature is direction-independent to match requests and responses.
func (p *Parser) idToCacheKey(id interface{}, processChain *event.ProcessChain) string {
	var baseKey string
	switch v := id.(type) {
	case int64:
		baseKey = fmt.Sprintf("i:%d", v)
	default:
		// String (or any other type treated as string)
		baseKey = fmt.Sprintf("s:%v", v)
	}

	// Add process chain correlation signature to make the key unique per process pair
	// Use CorrelationSignature (not Signature) to ensure request/response pairing works
	if processChain != nil {
		corrSig := processChain.CorrelationSignature()
		if corrSig != "" {
			return fmt.Sprintf("%s|%s", baseKey, corrSig)
		}
	}

	return baseKey
}

// cacheRequestMessage stores a request message for future response correlation.
// The cache key includes the process chain signature to prevent cross-process correlation issues.
func (p *Parser) cacheRequestMessage(msg *event.JSONRPCMessage, processChain *event.ProcessChain) error {
	if msg == nil || msg.ID == nil {
		return fmt.Errorf("invalid message")
	}
	if msg.Request != nil {
		// This shouldn't happen. Only responses should have Request field set.
		return fmt.Errorf("message already has Request field set")
	}
	key := p.idToCacheKey(msg.ID, processChain)
	p.requestIDCache.Add(key, msg)

	logrus.WithFields(logrus.Fields{
		"id":        msg.ID,
		"cache_key": key,
		"method":    msg.Method,
	}).Trace("Cached request message for correlation")

	return nil
}

// getRequestByID retrieves a cached request message by its ID and process chain.
// Returns the request message and true if found, nil and false otherwise.
func (p *Parser) getRequestByID(id interface{}, processChain *event.ProcessChain) (*event.JSONRPCMessage, bool) {
	if id == nil {
		return nil, false
	}
	key := p.idToCacheKey(id, processChain)
	req, exists := p.requestIDCache.Get(key)

	logrus.WithFields(logrus.Fields{
		"id":        id,
		"cache_key": key,
		"found":     exists,
	}).Trace("Looked up request message for correlation")

	return req, exists
}

// handleRequestResponseCorrelation handles caching request messages and pairing responses with their requests.
// For request messages, it caches the full message for future correlation.
// For response messages, it looks up and attaches the corresponding request.
// The process chain is used to ensure correlation happens within the same process context (STDIO only).
// For HTTP transport, we don't use process chains because requests and responses come from different PIDs.
func (p *Parser) handleRequestResponseCorrelation(msg *event.JSONRPCMessage, processChain *event.ProcessChain, isHTTP bool) error {
	// For HTTP, ignore process chain (requests and responses come from different PIDs)
	chainForCorrelation := processChain
	if isHTTP {
		chainForCorrelation = nil
	}

	switch msg.MessageType {
	case event.JSONRPCMessageTypeRequest:
		// Cache the full request message for future response pairing
		return p.cacheRequestMessage(msg, chainForCorrelation)
	case event.JSONRPCMessageTypeResponse:
		// Look up the corresponding request and attach it to the response
		req, exists := p.getRequestByID(msg.ID, chainForCorrelation)
		if !exists {
			// Drop responses without matching requests
			return fmt.Errorf("response without matching request ID")
		}
		// Attach the request to the response
		msg.Request = req
		return nil
	}
	// Notifications don't have IDs, always keep them
	return nil
}

// getOrCreateMessageMetadata retrieves or creates metadata for a message.
// If this is the first time we see this content hash, it creates new metadata.
// If we've seen it before, it adds the new process hop to the existing chain.
// Returns the metadata and a boolean indicating if this is a new unique message (true) or a duplicate hop (false).
func (p *Parser) getOrCreateMessageMetadata(hash string, hop event.ProcessHop) (*messageMetadata, bool) {
	metadata, exists := p.seenHashCache.Get(hash)
	if exists {
		// We've seen this message before - add the new hop to the chain
		added := metadata.processChain.AddHop(hop)
		if added {
			logrus.WithFields(logrus.Fields{
				"hash":      hash,
				"from_pid":  hop.FromPID,
				"to_pid":    hop.ToPID,
				"chain_sig": metadata.processChain.Signature(),
			}).Trace("Added new hop to existing message chain")
		}
		// Return false to indicate this is a duplicate (we've seen this content before)
		return metadata, false
	}

	// First time seeing this message - create new metadata
	metadata = &messageMetadata{
		contentHash: hash,
		processChain: &event.ProcessChain{
			Hops: []event.ProcessHop{hop},
		},
		firstSeen: time.Now(),
	}
	p.seenHashCache.Add(hash, metadata)

	logrus.WithFields(logrus.Fields{
		"hash":      hash,
		"from_pid":  hop.FromPID,
		"to_pid":    hop.ToPID,
		"chain_sig": metadata.processChain.Signature(),
	}).Trace("Created new message metadata with first hop")

	// Return true to indicate this is a new unique message
	return metadata, true
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
