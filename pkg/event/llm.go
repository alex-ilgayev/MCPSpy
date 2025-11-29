package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// LLMMessageType represents the type of LLM message
type LLMMessageType string

const (
	LLMMessageTypeRequest     LLMMessageType = "request"
	LLMMessageTypeResponse    LLMMessageType = "response"
	LLMMessageTypeStreamChunk LLMMessageType = "stream_chunk"
	LLMMessageTypeStreamEnd   LLMMessageType = "stream_end"
)

// LLMEvent represents a parsed Anthropic API message
type LLMEvent struct {
	Timestamp   time.Time      `json:"timestamp"`
	MessageType LLMMessageType `json:"message_type"`

	// Transport info
	PID  uint32 `json:"pid"`
	Comm string `json:"comm"`

	// Request/Response metadata
	Model       string `json:"model,omitempty"`
	IsStreaming bool   `json:"is_streaming"`

	// Messages in the conversation
	Messages []LLMMessage `json:"messages,omitempty"`

	// Tool calls (if any)
	ToolCalls []LLMToolCall `json:"tool_calls,omitempty"`

	// Response metadata
	StopReason   string `json:"stop_reason,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`

	// Stream chunk content
	ChunkContent string `json:"chunk_content,omitempty"`

	// Error message
	Error string `json:"error,omitempty"`
}

// LLMMessage represents a single message in the conversation
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// LLMToolCall represents a tool call made by the LLM
type LLMToolCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
}

func (e *LLMEvent) Type() EventType { return EventTypeLLMMessage }

func (e *LLMEvent) LogFields() logrus.Fields {
	fields := logrus.Fields{
		"message_type": e.MessageType,
		"model":        e.Model,
		"is_streaming": e.IsStreaming,
		"pid":          e.PID,
		"comm":         e.Comm,
	}

	if e.StopReason != "" {
		fields["stop_reason"] = e.StopReason
	}

	if e.InputTokens > 0 {
		fields["input_tokens"] = e.InputTokens
	}
	if e.OutputTokens > 0 {
		fields["output_tokens"] = e.OutputTokens
	}

	if e.Error != "" {
		fields["error"] = e.Error
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
	for i := len(e.Messages) - 1; i >= 0; i-- {
		if e.Messages[i].Role == "user" {
			return e.Messages[i].Content
		}
	}
	return ""
}
