package providers

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
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

// AnthropicParser parses Anthropic Claude API requests and responses
type AnthropicParser struct{}

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
