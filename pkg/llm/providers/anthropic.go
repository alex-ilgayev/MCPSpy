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

// Anthropic Messages API Request Structure
type anthropicRequest struct {
	Model       string              `json:"model"`
	Messages    []anthropicMessage  `json:"messages"`
	MaxTokens   int                 `json:"max_tokens"`
	Stream      bool                `json:"stream"`
	System      interface{}         `json:"system,omitempty"` // Can be string or array of content blocks
	Temperature float64             `json:"temperature,omitempty"`
	TopP        float64             `json:"top_p,omitempty"`
	TopK        int                 `json:"top_k,omitempty"`
	StopSeq     []string            `json:"stop_sequences,omitempty"`
	Tools       []anthropicTool     `json:"tools,omitempty"`
	ToolChoice  interface{}         `json:"tool_choice,omitempty"`
	Metadata    map[string]string   `json:"metadata,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or array of content blocks
}

type anthropicTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

// Anthropic Messages API Response Structure
type anthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence string                  `json:"stop_sequence,omitempty"`
	Usage        *anthropicUsage         `json:"usage,omitempty"`
	Error        *anthropicError         `json:"error,omitempty"`
}

type anthropicContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Anthropic Streaming Event Types
type anthropicStreamEvent struct {
	Type string `json:"type"`
	// message_start
	Message *anthropicResponse `json:"message,omitempty"`
	// content_block_start
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	// content_block_delta
	Delta *anthropicDelta `json:"delta,omitempty"`
	// message_delta
	Usage *anthropicUsage `json:"usage,omitempty"`
	// error
	Error *anthropicError `json:"error,omitempty"`
}

type anthropicDelta struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

// ParseRequest parses an Anthropic API request
func (p *AnthropicParser) ParseRequest(body []byte, transport event.LLMTransport, endpoint string) (*event.LLMEvent, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderAnthropic,
		Transport:   transport,
		MessageType: event.LLMMessageTypeRequest,
		Model:       req.Model,
		Endpoint:    endpoint,
		IsStreaming: req.Stream,
		Messages:    convertAnthropicMessages(req.Messages, req.System),
		Raw:         string(body),
	}

	return llmEvent, nil
}

// ParseResponse parses an Anthropic API response (non-streaming)
func (p *AnthropicParser) ParseResponse(body []byte, transport event.LLMTransport, endpoint string, statusCode int) (*event.LLMEvent, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderAnthropic,
		Transport:   transport,
		MessageType: event.LLMMessageTypeResponse,
		Model:       resp.Model,
		Endpoint:    endpoint,
		IsStreaming: false,
		RequestID:   resp.ID,
		StatusCode:  statusCode,
		Raw:         string(body),
	}

	// Handle error response
	if resp.Error != nil {
		llmEvent.Error = &event.LLMError{
			Type:    resp.Error.Type,
			Message: resp.Error.Message,
		}
		return llmEvent, nil
	}

	// Extract response content
	llmEvent.Messages, llmEvent.ToolCalls = extractAnthropicContent(resp.Content)
	llmEvent.FinishReason = resp.StopReason

	// Extract usage
	if resp.Usage != nil {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}

	return llmEvent, nil
}

// ParseStreamEvent parses an Anthropic streaming SSE event
func (p *AnthropicParser) ParseStreamEvent(eventType, data string, transport event.LLMTransport, endpoint string, chunkIndex int) (*event.LLMEvent, error) {
	var streamEvent anthropicStreamEvent
	if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderAnthropic,
		Transport:   transport,
		Endpoint:    endpoint,
		IsStreaming: true,
		ChunkIndex:  chunkIndex,
		Raw:         data,
	}

	switch streamEvent.Type {
	case "message_start":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Message != nil {
			llmEvent.Model = streamEvent.Message.Model
			llmEvent.RequestID = streamEvent.Message.ID
			if streamEvent.Message.Usage != nil {
				llmEvent.Usage = &event.LLMUsage{
					InputTokens: streamEvent.Message.Usage.InputTokens,
				}
			}
		}

	case "content_block_start":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.ContentBlock != nil {
			if streamEvent.ContentBlock.Type == "tool_use" {
				llmEvent.ToolCalls = []event.LLMToolCall{
					{
						ID:   streamEvent.ContentBlock.ID,
						Name: streamEvent.ContentBlock.Name,
					},
				}
			}
		}

	case "content_block_delta":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Delta != nil {
			switch streamEvent.Delta.Type {
			case "text_delta":
				llmEvent.ChunkContent = streamEvent.Delta.Text
			case "input_json_delta":
				llmEvent.ChunkContent = streamEvent.Delta.PartialJSON
			}
		}

	case "content_block_stop":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk

	case "message_delta":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Delta != nil {
			llmEvent.FinishReason = streamEvent.Delta.StopReason
		}
		if streamEvent.Usage != nil {
			llmEvent.Usage = &event.LLMUsage{
				OutputTokens: streamEvent.Usage.OutputTokens,
			}
		}

	case "message_stop":
		llmEvent.MessageType = event.LLMMessageTypeStreamEnd

	case "ping":
		// Ignore ping events
		return nil, nil

	case "error":
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
		if streamEvent.Error != nil {
			llmEvent.Error = &event.LLMError{
				Type:    streamEvent.Error.Type,
				Message: streamEvent.Error.Message,
			}
		}

	default:
		llmEvent.MessageType = event.LLMMessageTypeStreamChunk
	}

	return llmEvent, nil
}

// convertAnthropicMessages converts Anthropic messages to LLMMessage format
func convertAnthropicMessages(messages []anthropicMessage, system interface{}) []event.LLMMessage {
	result := make([]event.LLMMessage, 0, len(messages)+1)

	// Add system message if present
	if system != nil {
		systemContent := extractAnthropicMessageContent(system)
		if systemContent != "" {
			result = append(result, event.LLMMessage{
				Role:    "system",
				Content: systemContent,
			})
		}
	}

	for _, msg := range messages {
		result = append(result, event.LLMMessage{
			Role:    msg.Role,
			Content: extractAnthropicMessageContent(msg.Content),
		})
	}
	return result
}

// extractAnthropicMessageContent extracts string content from Anthropic message content
func extractAnthropicMessageContent(content interface{}) string {
	if content == nil {
		return ""
	}

	// Simple string content
	if s, ok := content.(string); ok {
		return s
	}

	// Array of content blocks
	if blocks, ok := content.([]interface{}); ok {
		var textParts []string
		for _, block := range blocks {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockMap["type"] == "text" {
					if text, ok := blockMap["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		return strings.Join(textParts, "\n")
	}

	return ""
}

// extractAnthropicContent extracts messages and tool calls from content blocks
func extractAnthropicContent(blocks []anthropicContentBlock) ([]event.LLMMessage, []event.LLMToolCall) {
	var textParts []string
	var toolCalls []event.LLMToolCall

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, event.LLMToolCall{
				ID:        block.ID,
				Type:      "function",
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	var messages []event.LLMMessage
	if len(textParts) > 0 {
		messages = []event.LLMMessage{
			{
				Role:    "assistant",
				Content: strings.Join(textParts, "\n"),
			},
		}
	}

	return messages, toolCalls
}
