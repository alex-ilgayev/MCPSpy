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
type anthropicResponse struct {
	Model   string                  `json:"model"`
	Content []anthropicContentBlock `json:"content"`
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

type anthropicDelta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
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
		Content:     extractUserPrompt(req.Messages),
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
	}

	if resp.Error != nil {
		ev.Error = resp.Error.Message
		return ev, nil
	}

	ev.Content = extractResponseText(resp.Content)
	return ev, nil
}

// ParseStreamEvent parses an Anthropic streaming SSE event
// Returns nil event for events we want to skip (ping, deltas, etc.)
// Returns event only for message_start (with model) and message_stop (final)
func (p *AnthropicParser) ParseStreamEvent(data string, pid uint32, comm string) (*event.LLMEvent, bool, error) {
	var streamEvent anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
		return nil, false, err
	}

	switch streamEvent.Type {
	case "message_start":
		// Return model info, continue accumulating
		ev := &event.LLMEvent{
			Timestamp:   time.Now(),
			MessageType: event.LLMMessageTypeResponse,
			PID:         pid,
			Comm:        comm,
		}
		if streamEvent.Message != nil {
			ev.Model = streamEvent.Message.Model
		}
		return ev, false, nil // false = not done

	case "content_block_delta":
		// Return delta text for accumulation, but don't emit event
		if streamEvent.Delta != nil && streamEvent.Delta.Type == "text_delta" {
			return &event.LLMEvent{Content: streamEvent.Delta.Text}, false, nil
		}
		return nil, false, nil

	case "message_stop":
		// Stream ended
		return nil, true, nil // true = done

	case "error":
		ev := &event.LLMEvent{
			Timestamp:   time.Now(),
			MessageType: event.LLMMessageTypeResponse,
			PID:         pid,
			Comm:        comm,
		}
		if streamEvent.Error != nil {
			ev.Error = streamEvent.Error.Message
		}
		return ev, true, nil

	default:
		// Ignore ping, content_block_start, content_block_stop, message_delta
		return nil, false, nil
	}
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
