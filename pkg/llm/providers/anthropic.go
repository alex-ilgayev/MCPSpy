package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

const (
	// emittedResultsTTL is the time-to-live for entries in emittedResults map
	// After this duration, entries are eligible for cleanup
	emittedResultsTTL = 10 * time.Minute
)

// AnthropicStreamEventType represents SSE event types in Anthropic's streaming API.
// Events are sent in order: message_start → content_block_start → content_block_delta(s) →
// content_block_stop → message_delta → message_stop
type AnthropicStreamEventType string

const (
	// AnthropicStreamEventTypeMessageStart signals the start of a new message.
	// Contains the message metadata including model, id, and initial usage stats.
	// This is always the first event in a stream.
	//
	// Example: {"type":"message_start","message":{"model":"claude-haiku-4-5-20251001","id":"msg_01HiWiRka7cogGJfB43zxyci","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":20012,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard"}}}
	AnthropicStreamEventTypeMessageStart AnthropicStreamEventType = "message_start"

	// AnthropicStreamEventTypeContentBlockStart signals the start of a content block.
	// Contains the block index and initial content_block structure (type: "text", empty text).
	// Sent before any deltas for that block.
	//
	// Example: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
	AnthropicStreamEventTypeContentBlockStart AnthropicStreamEventType = "content_block_start"

	// AnthropicStreamEventTypeContentBlockDelta contains incremental text content.
	// The delta field contains {"type": "text_delta", "text": "..."} with the actual tokens.
	// Multiple deltas are sent per content block as tokens are generated.
	//
	// Example: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hey"}}
	AnthropicStreamEventTypeContentBlockDelta AnthropicStreamEventType = "content_block_delta"

	// AnthropicStreamEventTypeContentBlockStop signals the end of a content block.
	// Contains only the block index. Sent after all deltas for that block.
	//
	// Example: {"type":"content_block_stop","index":0}
	AnthropicStreamEventTypeContentBlockStop AnthropicStreamEventType = "content_block_stop"

	// AnthropicStreamEventTypeMessageDelta contains final message metadata.
	// Includes stop_reason ("end_turn", "max_tokens", etc.) and final usage statistics.
	// Sent after all content blocks are complete.
	//
	// Example: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":20012,"output_tokens":80},"context_management":{"applied_edits":[]}}
	AnthropicStreamEventTypeMessageDelta AnthropicStreamEventType = "message_delta"

	// AnthropicStreamEventTypeMessageStop signals the end of the message stream.
	// Contains no additional data. This is always the last event in a successful stream.
	//
	// Example: {"type":"message_stop"}
	AnthropicStreamEventTypeMessageStop AnthropicStreamEventType = "message_stop"

	// AnthropicStreamEventTypePing is a keep-alive event sent periodically.
	// Contains no data. Used to prevent connection timeouts during long generations.
	//
	// Example: {"type": "ping"}
	AnthropicStreamEventTypePing AnthropicStreamEventType = "ping"

	// AnthropicStreamEventTypeError signals an error during streaming.
	// Contains error details in the error field. Terminates the stream.
	//
	// Example: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}
	AnthropicStreamEventTypeError AnthropicStreamEventType = "error"
)

// streamingToolBlock tracks a tool_use block being accumulated during streaming
type streamingToolBlock struct {
	sessionID uint64
	id        string
	name      string
	input     strings.Builder
}

// emittedResultEntry tracks when a tool result was emitted for TTL-based cleanup
type emittedResultEntry struct {
	timestamp time.Time
}

// AnthropicParser parses Anthropic Claude API requests and responses
type AnthropicParser struct {
	// toolNames maps "sessionID:tool_use_id" to tool name for correlating tool results
	toolNames sync.Map
	// streamingTools maps "sessionID:index" to in-progress tool_use blocks
	streamingTools sync.Map
	// emittedResults maps "sessionID:tool_use_id" to emittedResultEntry for deduplication
	// Entries are cleaned up after emittedResultsTTL
	emittedResults sync.Map
}

func NewAnthropicParser() *AnthropicParser {
	return &AnthropicParser{}
}

// Request structure (minimal)
type anthropicRequest struct {
	Model    string             `json:"model"`
	Messages []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// Response structure (minimal)
// Note: Error responses have a different structure with "type": "error" at root level
type anthropicResponse struct {
	Type    string                  `json:"type,omitempty"` // "message" for success, "error" for errors
	Model   string                  `json:"model,omitempty"`
	Content []anthropicContentBlock `json:"content,omitempty"`
	Error   *anthropicError         `json:"error,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type anthropicError struct {
	Message string `json:"message"`
}

// Streaming event structure (minimal)
type anthropicStreamEvent struct {
	Type    string             `json:"type"`
	Message *anthropicResponse `json:"message,omitempty"`
	Delta   *anthropicDelta    `json:"delta,omitempty"`
	Error   *anthropicError    `json:"error,omitempty"`
}

// anthropicDelta represents the delta in content_block_delta events.
// Note: message_delta events have a different delta structure (stop_reason, stop_sequence)
// which we ignore, so we don't need to parse those fields.
type anthropicDelta struct {
	Type string `json:"type"` // "text_delta" for text content
	Text string `json:"text,omitempty"`
}

// ParseRequest parses an Anthropic API request
func (p *AnthropicParser) ParseRequest(req *event.HttpRequestEvent) (*event.LLMEvent, error) {
	var anthropicReq anthropicRequest
	if err := json.Unmarshal(req.RequestPayload, &anthropicReq); err != nil {
		return nil, err
	}

	return &event.LLMEvent{
		SessionID:   req.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeRequest,
		PID:         req.PID,
		Comm:        req.Comm(),
		Host:        req.Host,
		Path:        req.Path,
		Model:       anthropicReq.Model,
		Content:     extractUserPrompt(anthropicReq.Messages),
		RawJSON:     string(req.RequestPayload),
	}, nil
}

// ParseResponse parses an Anthropic API response (non-streaming)
func (p *AnthropicParser) ParseResponse(resp *event.HttpResponseEvent) (*event.LLMEvent, error) {
	var anthropicResp anthropicResponse
	if err := json.Unmarshal(resp.ResponsePayload, &anthropicResp); err != nil {
		return nil, err
	}

	ev := &event.LLMEvent{
		SessionID:   resp.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeResponse,
		PID:         resp.PID,
		Comm:        resp.Comm(),
		Host:        resp.Host,
		Path:        resp.Path,
		Model:       anthropicResp.Model,
		RawJSON:     string(resp.ResponsePayload),
	}

	// Check for error response (type: "error" at root level)
	if anthropicResp.Type == "error" && anthropicResp.Error != nil {
		ev.Error = anthropicResp.Error.Message
		return ev, nil
	}

	ev.Content = extractResponseText(anthropicResp.Content)
	return ev, nil
}

// ParseStreamEvent parses an Anthropic streaming SSE event
// Returns: event (may be nil for skip), done flag, error
func (p *AnthropicParser) ParseStreamEvent(sse *event.SSEEvent) (*event.LLMEvent, bool, error) {
	data := strings.TrimSpace(string(sse.Data))
	if data == "" {
		return nil, false, nil
	}

	var streamEvent anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
		return nil, false, err
	}

	// Check for stream completion
	done := AnthropicStreamEventType(streamEvent.Type) == AnthropicStreamEventTypeMessageStop

	// Build event by extracting available fields
	ev := &event.LLMEvent{
		SessionID:   sse.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeStreamChunk,
		PID:         sse.PID,
		Comm:        sse.Comm(),
		Host:        sse.Host,
		Path:        sse.Path,
		RawJSON:     data, // Original SSE JSON payload
	}

	// Extract model from message_start
	if streamEvent.Message != nil && streamEvent.Message.Model != "" {
		ev.Model = streamEvent.Message.Model
	}

	// Extract text content from content_block_delta
	if streamEvent.Delta != nil && streamEvent.Delta.Type == "text_delta" && streamEvent.Delta.Text != "" {
		ev.Content = streamEvent.Delta.Text
	}

	// Extract error (terminates stream)
	if streamEvent.Error != nil && streamEvent.Error.Message != "" {
		ev.Error = streamEvent.Error.Message
		return ev, true, nil
	}

	return ev, done, nil
}

func extractUserPrompt(messages []anthropicMessage) string {
	// Get the last user message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return extractMessageContent(messages[i].Content)
		}
	}
	return ""
}

func extractMessageContent(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	// Array of content blocks
	if blocks, ok := content.([]interface{}); ok {
		var texts []string
		for _, block := range blocks {
			if m, ok := block.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func extractResponseText(blocks []anthropicContentBlock) string {
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// Tool usage extraction structures

// anthropicToolUseBlock represents a tool_use content block in responses
type anthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// anthropicToolResultBlock represents a tool_result content block in requests
type anthropicToolResultBlock struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error,omitempty"`
}

// anthropicResponseForTools is used to parse responses for tool_use blocks
type anthropicResponseForTools struct {
	Content []json.RawMessage `json:"content"`
}

// anthropicRequestForTools is used to parse requests for tool_result blocks
type anthropicRequestForTools struct {
	Messages []struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	} `json:"messages"`
}

// ExtractToolUsage extracts tool usage events from HTTP events.
// Accepts *event.HttpRequestEvent (for tool results), *event.HttpResponseEvent (for tool invocations),
// or *event.SSEEvent (for streaming tool invocations).
func (p *AnthropicParser) ExtractToolUsage(e event.Event) []*event.ToolUsageEvent {
	switch ev := e.(type) {
	case *event.HttpRequestEvent:
		return p.extractToolResults(ev.RequestPayload, ev.SSLContext)
	case *event.HttpResponseEvent:
		return p.extractToolCalls(ev.ResponsePayload, ev.SSLContext)
	case *event.SSEEvent:
		return p.extractToolUsageFromSSE(ev)
	default:
		return nil
	}
}

// extractToolCalls extracts tool_use blocks from response content
func (p *AnthropicParser) extractToolCalls(payload []byte, sessionID uint64) []*event.ToolUsageEvent {
	var resp anthropicResponseForTools
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil
	}

	var events []*event.ToolUsageEvent
	for _, rawBlock := range resp.Content {
		// First check the type
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawBlock, &typeCheck); err != nil {
			continue
		}
		if typeCheck.Type != "tool_use" {
			continue
		}

		// Parse as tool_use block
		var block anthropicToolUseBlock
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			continue
		}

		// Store tool name for correlation with tool_result (session-scoped key)
		toolNameKey := fmt.Sprintf("%d:%s", sessionID, block.ID)
		p.toolNames.Store(toolNameKey, block.Name)

		events = append(events, &event.ToolUsageEvent{
			SessionID: sessionID,
			Timestamp: time.Now(),
			UsageType: event.ToolUsageTypeInvocation,
			ToolID:    block.ID,
			ToolName:  block.Name,
			Input:     string(block.Input),
		})
	}

	return events
}

// extractToolResults extracts tool_result blocks from request messages.
// Uses deduplication by tool_use_id to avoid emitting the same result multiple times
// as conversation history accumulates.
func (p *AnthropicParser) extractToolResults(payload []byte, sessionID uint64) []*event.ToolUsageEvent {
	// Cleanup expired entries before processing
	p.cleanupExpiredResults()

	var req anthropicRequestForTools
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil
	}

	var events []*event.ToolUsageEvent
	for _, msg := range req.Messages {
		// tool_result blocks appear in user messages
		if msg.Role != "user" {
			continue
		}

		for _, rawBlock := range msg.Content {
			// First check the type
			var typeCheck struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(rawBlock, &typeCheck); err != nil {
				continue
			}
			if typeCheck.Type != "tool_result" {
				continue
			}

			// Parse as tool_result block
			var block anthropicToolResultBlock
			if err := json.Unmarshal(rawBlock, &block); err != nil {
				continue
			}

			// Skip if we've already emitted this result (dedup with session-scoped key)
			emittedKey := fmt.Sprintf("%d:%s", sessionID, block.ToolUseID)
			entry := emittedResultEntry{timestamp: time.Now()}
			if _, alreadyEmitted := p.emittedResults.LoadOrStore(emittedKey, entry); alreadyEmitted {
				continue
			}

			// Look up tool name from previous tool_use (session-scoped key)
			toolNameKey := fmt.Sprintf("%d:%s", sessionID, block.ToolUseID)
			toolName := ""
			if name, ok := p.toolNames.Load(toolNameKey); ok {
				toolName = name.(string)
				// Clean up after use to prevent memory growth
				p.toolNames.Delete(toolNameKey)
			}

			events = append(events, &event.ToolUsageEvent{
				SessionID: sessionID,
				Timestamp: time.Now(),
				UsageType: event.ToolUsageTypeResult,
				ToolID:    block.ToolUseID,
				ToolName:  toolName,
				Output:    string(block.Content),
				IsError:   block.IsError,
			})
		}
	}

	return events
}

// cleanupExpiredResults removes entries from emittedResults that are older than emittedResultsTTL
func (p *AnthropicParser) cleanupExpiredResults() {
	now := time.Now()
	p.emittedResults.Range(func(key, value interface{}) bool {
		if entry, ok := value.(emittedResultEntry); ok {
			if now.Sub(entry.timestamp) > emittedResultsTTL {
				p.emittedResults.Delete(key)
			}
		}
		return true
	})
}

// SSE event structures for tool extraction

type sseContentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
}

type sseContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type          string `json:"type"`
		PartialJSON   string `json:"partial_json,omitempty"`
		InputJSONText string `json:"input_json_delta,omitempty"` // Anthropic uses this field name
	} `json:"delta"`
}

type sseContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// extractToolUsageFromSSE extracts tool usage from streaming SSE events
func (p *AnthropicParser) extractToolUsageFromSSE(sse *event.SSEEvent) []*event.ToolUsageEvent {
	data := strings.TrimSpace(string(sse.Data))
	if data == "" {
		return nil
	}

	// First, determine the event type
	var typeCheck struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(data), &typeCheck); err != nil {
		return nil
	}

	switch AnthropicStreamEventType(typeCheck.Type) {
	case AnthropicStreamEventTypeContentBlockStart:
		return p.handleContentBlockStart(data, sse.SSLContext)
	case AnthropicStreamEventTypeContentBlockDelta:
		p.handleContentBlockDelta(data, sse.SSLContext)
		return nil
	case AnthropicStreamEventTypeContentBlockStop:
		return p.handleContentBlockStop(data, sse.SSLContext)
	default:
		return nil
	}
}

func (p *AnthropicParser) handleContentBlockStart(data string, sessionID uint64) []*event.ToolUsageEvent {
	var ev sseContentBlockStart
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil
	}

	// Only track tool_use blocks
	if ev.ContentBlock.Type != "tool_use" {
		return nil
	}

	// Create a new streaming tool block
	block := &streamingToolBlock{
		sessionID: sessionID,
		id:        ev.ContentBlock.ID,
		name:      ev.ContentBlock.Name,
	}

	// Store for delta accumulation (session-scoped key)
	streamingKey := fmt.Sprintf("%d:%d", sessionID, ev.Index)
	p.streamingTools.Store(streamingKey, block)

	// Store tool name for result correlation (session-scoped key)
	toolNameKey := fmt.Sprintf("%d:%s", sessionID, ev.ContentBlock.ID)
	p.toolNames.Store(toolNameKey, ev.ContentBlock.Name)

	return nil
}

func (p *AnthropicParser) handleContentBlockDelta(data string, sessionID uint64) {
	var ev sseContentBlockDelta
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}

	// Only process input_json_delta
	if ev.Delta.Type != "input_json_delta" {
		return
	}

	// Look up the streaming tool block (session-scoped key)
	streamingKey := fmt.Sprintf("%d:%d", sessionID, ev.Index)
	blockI, ok := p.streamingTools.Load(streamingKey)
	if !ok {
		return
	}
	block := blockI.(*streamingToolBlock)

	// Accumulate input JSON
	// The delta contains raw JSON text to append
	if ev.Delta.PartialJSON != "" {
		block.input.WriteString(ev.Delta.PartialJSON)
	}
}

func (p *AnthropicParser) handleContentBlockStop(data string, sessionID uint64) []*event.ToolUsageEvent {
	var ev sseContentBlockStop
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil
	}

	// Look up and remove the streaming tool block (session-scoped key)
	streamingKey := fmt.Sprintf("%d:%d", sessionID, ev.Index)
	blockI, ok := p.streamingTools.LoadAndDelete(streamingKey)
	if !ok {
		return nil
	}
	block := blockI.(*streamingToolBlock)

	// Emit the completed tool invocation event
	return []*event.ToolUsageEvent{{
		SessionID: block.sessionID,
		Timestamp: time.Now(),
		UsageType: event.ToolUsageTypeInvocation,
		ToolID:    block.id,
		ToolName:  block.name,
		Input:     block.input.String(),
	}}
}
