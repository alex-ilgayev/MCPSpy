package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// LLMProvider represents an LLM API provider
type LLMProvider string

const (
	ProviderOpenAI    LLMProvider = "openai"
	ProviderAnthropic LLMProvider = "anthropic"
	ProviderGemini    LLMProvider = "gemini"
	ProviderOllama    LLMProvider = "ollama"
	ProviderAzure     LLMProvider = "azure_openai"
)

// LLMMessageType represents the type of LLM message
type LLMMessageType string

const (
	LLMMessageTypeRequest     LLMMessageType = "request"
	LLMMessageTypeResponse    LLMMessageType = "response"
	LLMMessageTypeStreamChunk LLMMessageType = "stream_chunk"
	LLMMessageTypeStreamEnd   LLMMessageType = "stream_end"
)

// LLMTransport contains transport metadata for LLM API calls
type LLMTransport struct {
	PID  uint32 `json:"pid"`
	Comm string `json:"comm"`
	Host string `json:"host"`
}

// LLMMessage represents a single message in the conversation
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// LLMToolCall represents a tool/function call made by the LLM
type LLMToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// LLMUsage contains token usage statistics
type LLMUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// LLMEvent represents a parsed LLM API message (request or response)
type LLMEvent struct {
	Timestamp   time.Time      `json:"timestamp"`
	Provider    LLMProvider    `json:"provider"`
	Transport   LLMTransport   `json:"transport"`
	MessageType LLMMessageType `json:"message_type"`

	// Request/Response metadata
	Model       string `json:"model,omitempty"`
	Endpoint    string `json:"endpoint"`
	IsStreaming bool   `json:"is_streaming"`

	// Request ID for correlation (provider-specific)
	RequestID string `json:"request_id,omitempty"`

	// Messages in the conversation (request) or response content
	Messages []LLMMessage `json:"messages,omitempty"`

	// Tool calls (if any)
	ToolCalls []LLMToolCall `json:"tool_calls,omitempty"`

	// Response metadata
	FinishReason string    `json:"finish_reason,omitempty"`
	Usage        *LLMUsage `json:"usage,omitempty"`

	// Stream chunk content (for streaming responses)
	ChunkContent string `json:"chunk_content,omitempty"`
	ChunkIndex   int    `json:"chunk_index,omitempty"`

	// HTTP status code (for responses)
	StatusCode int `json:"status_code,omitempty"`

	// Error information
	Error *LLMError `json:"error,omitempty"`

	// Original raw payload
	Raw string `json:"raw,omitempty"`
}

// LLMError represents an error from the LLM API
type LLMError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (e *LLMEvent) Type() EventType { return EventTypeLLMMessage }

func (e *LLMEvent) LogFields() logrus.Fields {
	fields := logrus.Fields{
		"provider":     e.Provider,
		"message_type": e.MessageType,
		"model":        e.Model,
		"endpoint":     e.Endpoint,
		"is_streaming": e.IsStreaming,
	}

	// Add transport info
	fields["pid"] = e.Transport.PID
	fields["comm"] = e.Transport.Comm
	fields["host"] = e.Transport.Host

	// Add response metadata if present
	if e.FinishReason != "" {
		fields["finish_reason"] = e.FinishReason
	}

	if e.Usage != nil {
		fields["input_tokens"] = e.Usage.InputTokens
		fields["output_tokens"] = e.Usage.OutputTokens
	}

	if e.Error != nil {
		fields["error"] = e.Error.Message
	}

	if len(e.ToolCalls) > 0 {
		fields["tool_calls_count"] = len(e.ToolCalls)
	}

	return fields
}

// ExtractAssistantContent extracts the assistant's response content
func (e *LLMEvent) ExtractAssistantContent() string {
	for _, msg := range e.Messages {
		if msg.Role == "assistant" {
			return msg.Content
		}
	}
	return ""
}

// ExtractUserPrompt extracts the last user prompt from the request
func (e *LLMEvent) ExtractUserPrompt() string {
	// Iterate backwards to get the most recent user message
	for i := len(e.Messages) - 1; i >= 0; i-- {
		if e.Messages[i].Role == "user" {
			return e.Messages[i].Content
		}
	}
	return ""
}

// ExtractToolNames returns the names of all tool calls
func (e *LLMEvent) ExtractToolNames() []string {
	names := make([]string, 0, len(e.ToolCalls))
	for _, tc := range e.ToolCalls {
		names = append(names, tc.Name)
	}
	return names
}
