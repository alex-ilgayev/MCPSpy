package providers

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// OpenAIParser parses OpenAI API requests and responses
type OpenAIParser struct{}

func NewOpenAIParser() *OpenAIParser {
	return &OpenAIParser{}
}

// OpenAI Chat Completions Request Structure
type openAIRequest struct {
	Model            string                   `json:"model"`
	Messages         []openAIMessage          `json:"messages"`
	Stream           bool                     `json:"stream"`
	Temperature      float64                  `json:"temperature,omitempty"`
	MaxTokens        int                      `json:"max_tokens,omitempty"`
	Tools            []openAITool             `json:"tools,omitempty"`
	ToolChoice       interface{}              `json:"tool_choice,omitempty"`
	ResponseFormat   map[string]interface{}   `json:"response_format,omitempty"`
	N                int                      `json:"n,omitempty"`
	Stop             interface{}              `json:"stop,omitempty"`
	PresencePenalty  float64                  `json:"presence_penalty,omitempty"`
	FrequencyPenalty float64                  `json:"frequency_penalty,omitempty"`
}

type openAIMessage struct {
	Role       string               `json:"role"`
	Content    interface{}          `json:"content"` // Can be string or array of content parts
	Name       string               `json:"name,omitempty"`
	ToolCalls  []openAIToolCall     `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string           `json:"type"`
	Function openAIFunction   `json:"function"`
}

type openAIFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// OpenAI Chat Completions Response Structure
type openAIResponse struct {
	ID                string           `json:"id"`
	Object            string           `json:"object"`
	Created           int64            `json:"created"`
	Model             string           `json:"model"`
	Choices           []openAIChoice   `json:"choices"`
	Usage             *openAIUsage     `json:"usage,omitempty"`
	SystemFingerprint string           `json:"system_fingerprint,omitempty"`
	Error             *openAIError     `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        int              `json:"index"`
	Message      *openAIMessage   `json:"message,omitempty"`
	Delta        *openAIMessage   `json:"delta,omitempty"` // For streaming
	FinishReason string           `json:"finish_reason,omitempty"`
	Logprobs     interface{}      `json:"logprobs,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// OpenAI Streaming Chunk Structure
type openAIStreamChunk struct {
	ID                string           `json:"id"`
	Object            string           `json:"object"`
	Created           int64            `json:"created"`
	Model             string           `json:"model"`
	Choices           []openAIChoice   `json:"choices"`
	SystemFingerprint string           `json:"system_fingerprint,omitempty"`
	Usage             *openAIUsage     `json:"usage,omitempty"`
}

// ParseRequest parses an OpenAI API request
func (p *OpenAIParser) ParseRequest(body []byte, transport event.LLMTransport, endpoint string) (*event.LLMEvent, error) {
	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOpenAI,
		Transport:   transport,
		MessageType: event.LLMMessageTypeRequest,
		Model:       req.Model,
		Endpoint:    endpoint,
		IsStreaming: req.Stream,
		Messages:    convertOpenAIMessages(req.Messages),
		Raw:         string(body),
	}

	return llmEvent, nil
}

// ParseResponse parses an OpenAI API response (non-streaming)
func (p *OpenAIParser) ParseResponse(body []byte, transport event.LLMTransport, endpoint string, statusCode int) (*event.LLMEvent, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOpenAI,
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
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
		return llmEvent, nil
	}

	// Extract response content
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		if choice.Message != nil {
			llmEvent.Messages = []event.LLMMessage{
				{
					Role:    choice.Message.Role,
					Content: extractMessageContent(choice.Message.Content),
				},
			}

			// Extract tool calls if present
			if len(choice.Message.ToolCalls) > 0 {
				llmEvent.ToolCalls = convertOpenAIToolCalls(choice.Message.ToolCalls)
			}
		}
		llmEvent.FinishReason = choice.FinishReason
	}

	// Extract usage
	if resp.Usage != nil {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}
	}

	return llmEvent, nil
}

// ParseStreamChunk parses an OpenAI streaming chunk
func (p *OpenAIParser) ParseStreamChunk(data string, transport event.LLMTransport, endpoint string, chunkIndex int) (*event.LLMEvent, error) {
	// Handle the [DONE] marker
	if strings.TrimSpace(data) == "[DONE]" {
		return &event.LLMEvent{
			Timestamp:   time.Now(),
			Provider:    event.ProviderOpenAI,
			Transport:   transport,
			MessageType: event.LLMMessageTypeStreamEnd,
			Endpoint:    endpoint,
			IsStreaming: true,
			ChunkIndex:  chunkIndex,
		}, nil
	}

	var chunk openAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOpenAI,
		Transport:   transport,
		MessageType: event.LLMMessageTypeStreamChunk,
		Model:       chunk.Model,
		Endpoint:    endpoint,
		IsStreaming: true,
		RequestID:   chunk.ID,
		ChunkIndex:  chunkIndex,
		Raw:         data,
	}

	// Extract delta content
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta != nil {
			llmEvent.ChunkContent = extractMessageContent(choice.Delta.Content)

			// Extract tool calls from delta if present
			if len(choice.Delta.ToolCalls) > 0 {
				llmEvent.ToolCalls = convertOpenAIToolCalls(choice.Delta.ToolCalls)
			}
		}
		llmEvent.FinishReason = choice.FinishReason
	}

	// Extract usage (available in final chunk with stream_options)
	if chunk.Usage != nil {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens:  chunk.Usage.TotalTokens,
		}
	}

	return llmEvent, nil
}

// convertOpenAIMessages converts OpenAI messages to LLMMessage format
func convertOpenAIMessages(messages []openAIMessage) []event.LLMMessage {
	result := make([]event.LLMMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, event.LLMMessage{
			Role:    msg.Role,
			Content: extractMessageContent(msg.Content),
		})
	}
	return result
}

// extractMessageContent extracts string content from OpenAI message content
// which can be either a string or an array of content parts
func extractMessageContent(content interface{}) string {
	if content == nil {
		return ""
	}

	// Simple string content
	if s, ok := content.(string); ok {
		return s
	}

	// Array of content parts (multimodal)
	if parts, ok := content.([]interface{}); ok {
		var textParts []string
		for _, part := range parts {
			if partMap, ok := part.(map[string]interface{}); ok {
				if partMap["type"] == "text" {
					if text, ok := partMap["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		return strings.Join(textParts, "\n")
	}

	return ""
}

// convertOpenAIToolCalls converts OpenAI tool calls to LLMToolCall format
func convertOpenAIToolCalls(toolCalls []openAIToolCall) []event.LLMToolCall {
	result := make([]event.LLMToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		result = append(result, event.LLMToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return result
}
