package providers

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// AnthropicParser parses Anthropic Claude API requests and responses
type AnthropicParser struct{}

func NewAnthropicParser() *AnthropicParser {
	return &AnthropicParser{}
}

// Request structure
type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    interface{}        `json:"system,omitempty"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name string `json:"name"`
}

// Response structure
type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	Model      string                  `json:"model"`
	StopReason string                  `json:"stop_reason"`
	Usage      *anthropicUsage         `json:"usage,omitempty"`
	Error      *anthropicError         `json:"error,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Streaming event structure
type anthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Message      *anthropicResponse     `json:"message,omitempty"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *anthropicDelta        `json:"delta,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
	Error        *anthropicError        `json:"error,omitempty"`
}

type anthropicDelta struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ParseRequest parses an Anthropic API request
func (p *AnthropicParser) ParseRequest(body []byte, pid uint32, comm string) (*event.LLMEvent, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	return &event.LLMEvent{
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeRequest,
		PID:         pid,
		Comm:        comm,
		Model:       req.Model,
		IsStreaming: req.Stream,
		Messages:    convertMessages(req.Messages, req.System),
	}, nil
}

// ParseResponse parses an Anthropic API response (non-streaming)
func (p *AnthropicParser) ParseResponse(body []byte, pid uint32, comm string) (*event.LLMEvent, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	ev := &event.LLMEvent{
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeResponse,
		PID:         pid,
		Comm:        comm,
		Model:       resp.Model,
		IsStreaming: false,
		StopReason:  resp.StopReason,
	}

	if resp.Error != nil {
		ev.Error = resp.Error.Message
		return ev, nil
	}

	ev.Messages, ev.ToolCalls = extractContent(resp.Content)

	if resp.Usage != nil {
		ev.InputTokens = resp.Usage.InputTokens
		ev.OutputTokens = resp.Usage.OutputTokens
	}

	return ev, nil
}

// ParseStreamEvent parses an Anthropic streaming SSE event
func (p *AnthropicParser) ParseStreamEvent(data string, pid uint32, comm string) (*event.LLMEvent, error) {
	var streamEvent anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
		return nil, err
	}

	ev := &event.LLMEvent{
		Timestamp:   time.Now(),
		PID:         pid,
		Comm:        comm,
		IsStreaming: true,
	}

	switch streamEvent.Type {
	case "message_start":
		ev.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Message != nil {
			ev.Model = streamEvent.Message.Model
			if streamEvent.Message.Usage != nil {
				ev.InputTokens = streamEvent.Message.Usage.InputTokens
			}
		}

	case "content_block_start":
		ev.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.ContentBlock != nil && streamEvent.ContentBlock.Type == "tool_use" {
			ev.ToolCalls = []event.LLMToolCall{{
				ID:   streamEvent.ContentBlock.ID,
				Name: streamEvent.ContentBlock.Name,
			}}
		}

	case "content_block_delta":
		ev.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Delta != nil && streamEvent.Delta.Type == "text_delta" {
			ev.ChunkContent = streamEvent.Delta.Text
		}

	case "message_delta":
		ev.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Delta != nil {
			ev.StopReason = streamEvent.Delta.StopReason
		}
		if streamEvent.Usage != nil {
			ev.OutputTokens = streamEvent.Usage.OutputTokens
		}

	case "message_stop":
		ev.MessageType = event.LLMMessageTypeStreamEnd

	case "ping", "content_block_stop":
		return nil, nil // Ignore these events

	case "error":
		ev.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Error != nil {
			ev.Error = streamEvent.Error.Message
		}

	default:
		ev.MessageType = event.LLMMessageTypeStreamChunk
	}

	return ev, nil
}

func convertMessages(messages []anthropicMessage, system interface{}) []event.LLMMessage {
	result := make([]event.LLMMessage, 0, len(messages)+1)

	// Add system message if present
	if system != nil {
		if s, ok := system.(string); ok && s != "" {
			result = append(result, event.LLMMessage{Role: "system", Content: s})
		}
	}

	for _, msg := range messages {
		result = append(result, event.LLMMessage{
			Role:    msg.Role,
			Content: extractMessageContent(msg.Content),
		})
	}
	return result
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

func extractContent(blocks []anthropicContentBlock) ([]event.LLMMessage, []event.LLMToolCall) {
	var texts []string
	var toolCalls []event.LLMToolCall

	for _, block := range blocks {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, event.LLMToolCall{
				ID:   block.ID,
				Name: block.Name,
			})
		}
	}

	var messages []event.LLMMessage
	if len(texts) > 0 {
		messages = []event.LLMMessage{{
			Role:    "assistant",
			Content: strings.Join(texts, "\n"),
		}}
	}

	return messages, toolCalls
}
