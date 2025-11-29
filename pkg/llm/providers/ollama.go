package providers

import (
	"encoding/json"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// OllamaParser parses Ollama API requests and responses
type OllamaParser struct{}

func NewOllamaParser() *OllamaParser {
	return &OllamaParser{}
}

// Ollama /api/chat Request Structure
type ollamaRequest struct {
	Model     string             `json:"model"`
	Messages  []ollamaMessage    `json:"messages"`
	Stream    *bool              `json:"stream,omitempty"` // Defaults to true if not specified
	Format    interface{}        `json:"format,omitempty"` // "json" or JSON schema
	Options   map[string]interface{} `json:"options,omitempty"`
	Tools     []ollamaTool       `json:"tools,omitempty"`
	KeepAlive string             `json:"keep_alive,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaTool struct {
	Type     string         `json:"type"`
	Function ollamaFunction `json:"function"`
}

type ollamaFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

// Ollama /api/chat Response Structure
type ollamaResponse struct {
	Model              string         `json:"model"`
	CreatedAt          string         `json:"created_at"`
	Message            *ollamaMessage `json:"message,omitempty"`
	Done               bool           `json:"done"`
	DoneReason         string         `json:"done_reason,omitempty"`
	TotalDuration      int64          `json:"total_duration,omitempty"`
	LoadDuration       int64          `json:"load_duration,omitempty"`
	PromptEvalCount    int            `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64          `json:"prompt_eval_duration,omitempty"`
	EvalCount          int            `json:"eval_count,omitempty"`
	EvalDuration       int64          `json:"eval_duration,omitempty"`
	Error              string         `json:"error,omitempty"`
}

// ParseRequest parses an Ollama API request
func (p *OllamaParser) ParseRequest(body []byte, transport event.LLMTransport, endpoint string) (*event.LLMEvent, error) {
	var req ollamaRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// Ollama defaults to streaming if not specified
	isStreaming := true
	if req.Stream != nil {
		isStreaming = *req.Stream
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOllama,
		Transport:   transport,
		MessageType: event.LLMMessageTypeRequest,
		Model:       req.Model,
		Endpoint:    endpoint,
		IsStreaming: isStreaming,
		Messages:    convertOllamaMessages(req.Messages),
		Raw:         string(body),
	}

	return llmEvent, nil
}

// ParseResponse parses an Ollama API response (non-streaming or final streaming response)
func (p *OllamaParser) ParseResponse(body []byte, transport event.LLMTransport, endpoint string, statusCode int) (*event.LLMEvent, error) {
	var resp ollamaResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOllama,
		Transport:   transport,
		MessageType: event.LLMMessageTypeResponse,
		Model:       resp.Model,
		Endpoint:    endpoint,
		IsStreaming: false,
		StatusCode:  statusCode,
		Raw:         string(body),
	}

	// Handle error response
	if resp.Error != "" {
		llmEvent.Error = &event.LLMError{
			Message: resp.Error,
		}
		return llmEvent, nil
	}

	// Extract response content
	if resp.Message != nil {
		llmEvent.Messages = []event.LLMMessage{
			{
				Role:    resp.Message.Role,
				Content: resp.Message.Content,
			},
		}

		// Extract tool calls if present
		if len(resp.Message.ToolCalls) > 0 {
			llmEvent.ToolCalls = convertOllamaToolCalls(resp.Message.ToolCalls)
		}
	}

	// Set finish reason from done_reason
	llmEvent.FinishReason = resp.DoneReason
	if llmEvent.FinishReason == "" && resp.Done {
		llmEvent.FinishReason = "stop"
	}

	// Extract usage (Ollama provides token counts)
	if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  resp.PromptEvalCount,
			OutputTokens: resp.EvalCount,
			TotalTokens:  resp.PromptEvalCount + resp.EvalCount,
		}
	}

	return llmEvent, nil
}

// ParseStreamChunk parses an Ollama streaming chunk (newline-delimited JSON)
func (p *OllamaParser) ParseStreamChunk(data string, transport event.LLMTransport, endpoint string, chunkIndex int) (*event.LLMEvent, error) {
	var resp ollamaResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}

	msgType := event.LLMMessageTypeStreamChunk
	if resp.Done {
		msgType = event.LLMMessageTypeStreamEnd
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderOllama,
		Transport:   transport,
		MessageType: msgType,
		Model:       resp.Model,
		Endpoint:    endpoint,
		IsStreaming: true,
		ChunkIndex:  chunkIndex,
		Raw:         data,
	}

	// Handle error
	if resp.Error != "" {
		llmEvent.Error = &event.LLMError{
			Message: resp.Error,
		}
		return llmEvent, nil
	}

	// Extract chunk content
	if resp.Message != nil {
		llmEvent.ChunkContent = resp.Message.Content

		// Extract tool calls if present
		if len(resp.Message.ToolCalls) > 0 {
			llmEvent.ToolCalls = convertOllamaToolCalls(resp.Message.ToolCalls)
		}
	}

	// Set finish reason if done
	if resp.Done {
		llmEvent.FinishReason = resp.DoneReason
		if llmEvent.FinishReason == "" {
			llmEvent.FinishReason = "stop"
		}

		// Include final usage stats
		if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
			llmEvent.Usage = &event.LLMUsage{
				InputTokens:  resp.PromptEvalCount,
				OutputTokens: resp.EvalCount,
				TotalTokens:  resp.PromptEvalCount + resp.EvalCount,
			}
		}
	}

	return llmEvent, nil
}

// convertOllamaMessages converts Ollama messages to LLMMessage format
func convertOllamaMessages(messages []ollamaMessage) []event.LLMMessage {
	result := make([]event.LLMMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, event.LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return result
}

// convertOllamaToolCalls converts Ollama tool calls to LLMToolCall format
func convertOllamaToolCalls(toolCalls []ollamaToolCall) []event.LLMToolCall {
	result := make([]event.LLMToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		args, _ := json.Marshal(tc.Function.Arguments)
		result = append(result, event.LLMToolCall{
			Type:      "function",
			Name:      tc.Function.Name,
			Arguments: string(args),
		})
	}
	return result
}
