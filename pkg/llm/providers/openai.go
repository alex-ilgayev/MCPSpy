package providers

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// OpenAI streaming event types for SSE responses
// Events contain delta objects with content fragments
const (
	// OpenAI uses simple SSE format with data: prefix
	// Each chunk contains: {"choices":[{"delta":{"content":"token"}}]}
	// The final chunk has: {"choices":[{"finish_reason":"stop"}]}
	OpenAIStreamEventTypeDone = "[DONE]"
)

// OpenAIParser parses OpenAI API requests and responses
type OpenAIParser struct{}

func NewOpenAIParser() *OpenAIParser {
	return &OpenAIParser{}
}

// Request structure
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type openaiMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// Response structure (non-streaming)
type openaiResponse struct {
	ID      string         `json:"id,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []openaiChoice `json:"choices,omitempty"`
	Error   *openaiError   `json:"error,omitempty"`
}

type openaiChoice struct {
	Index        int            `json:"index"`
	Message      *openaiMessage `json:"message,omitempty"`
	Delta        *openaiDelta   `json:"delta,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
}

type openaiDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

// ParseRequest parses an OpenAI API request
func (p *OpenAIParser) ParseRequest(req *event.HttpRequestEvent) (*event.LLMEvent, error) {
	var openaiReq openaiRequest
	if err := json.Unmarshal(req.RequestPayload, &openaiReq); err != nil {
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
		Model:       openaiReq.Model,
		Content:     extractOpenAIUserPrompt(openaiReq.Messages),
		RawJSON:     string(req.RequestPayload),
	}, nil
}

// ParseResponse parses an OpenAI API response (non-streaming)
func (p *OpenAIParser) ParseResponse(resp *event.HttpResponseEvent) (*event.LLMEvent, error) {
	var openaiResp openaiResponse
	if err := json.Unmarshal(resp.ResponsePayload, &openaiResp); err != nil {
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
		Model:       openaiResp.Model,
		RawJSON:     string(resp.ResponsePayload),
	}

	// Check for error response
	if openaiResp.Error != nil && openaiResp.Error.Message != "" {
		ev.Error = openaiResp.Error.Message
		return ev, nil
	}

	ev.Content = extractOpenAIResponseText(openaiResp.Choices)
	return ev, nil
}

// ParseStreamEvent parses an OpenAI streaming SSE event
// Returns: event (may be nil for skip), done flag, error
func (p *OpenAIParser) ParseStreamEvent(sse *event.SSEEvent) (*event.LLMEvent, bool, error) {
	data := strings.TrimSpace(string(sse.Data))
	if data == "" {
		return nil, false, nil
	}

	// OpenAI signals stream completion with [DONE]
	if data == OpenAIStreamEventTypeDone {
		return nil, true, nil
	}

	var streamResp openaiResponse
	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return nil, false, err
	}

	ev := &event.LLMEvent{
		SessionID:   sse.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeStreamChunk,
		PID:         sse.PID,
		Comm:        sse.Comm(),
		Host:        sse.Host,
		Path:        sse.Path,
		Model:       streamResp.Model,
		RawJSON:     data,
	}

	// Check for error
	if streamResp.Error != nil && streamResp.Error.Message != "" {
		ev.Error = streamResp.Error.Message
		return ev, true, nil
	}

	// Extract content from delta
	if len(streamResp.Choices) > 0 && streamResp.Choices[0].Delta != nil {
		ev.Content = streamResp.Choices[0].Delta.Content
	}

	// Check for stream completion via finish_reason
	done := false
	if len(streamResp.Choices) > 0 && streamResp.Choices[0].FinishReason != "" {
		done = true
	}

	return ev, done, nil
}

// extractOpenAIUserPrompt extracts the user's prompt from the messages array
// Gets the last user message
func extractOpenAIUserPrompt(messages []openaiMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return extractOpenAIMessageContent(messages[i].Content)
		}
	}
	return ""
}

// extractOpenAIMessageContent extracts text from message content
// OpenAI content can be a string or an array of content parts
func extractOpenAIMessageContent(content interface{}) string {
	if content == nil {
		return ""
	}
	// Simple string content
	if s, ok := content.(string); ok {
		return s
	}
	// Array of content parts (for vision/multimodal)
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

// extractOpenAIResponseText extracts text from choices array
func extractOpenAIResponseText(choices []openaiChoice) string {
	if len(choices) == 0 {
		return ""
	}

	// Use first choice's message content
	if choices[0].Message != nil {
		return extractOpenAIMessageContent(choices[0].Message.Content)
	}
	return ""
}
