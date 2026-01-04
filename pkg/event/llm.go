package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// LLMMessageType represents the type of LLM message
type LLMMessageType string

const (
	LLMMessageTypeRequest     LLMMessageType = "request"      // User request to LLM API
	LLMMessageTypeStreamChunk LLMMessageType = "stream_chunk" // Streaming delta (partial content)
	LLMMessageTypeResponse    LLMMessageType = "response"     // Complete response (final)
)

// LLMEvent represents a parsed LLM API message
type LLMEvent struct {
	SessionID   uint64         `json:"session_id"`          // Correlates all events in the same HTTP session
	Timestamp   time.Time      `json:"timestamp"`
	MessageType LLMMessageType `json:"message_type"`
	PID         uint32         `json:"pid"`
	Comm        string         `json:"comm"`
	Host        string         `json:"host"`
	Path        string         `json:"path"`
	Model       string         `json:"model,omitempty"`
	Content     string         `json:"content,omitempty"` // Request: user prompt, StreamChunk: delta, Response: full content
	Error       string         `json:"error,omitempty"`
	RawJSON     string         `json:"raw_json,omitempty"` // Original HTTP payload JSON (for requests/responses, not stream chunks)
}

func (e *LLMEvent) Type() EventType { return EventTypeLLMMessage }

func (e *LLMEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"session_id":   e.SessionID,
		"message_type": e.MessageType,
		"model":        e.Model,
		"pid":          e.PID,
		"comm":         e.Comm,
	}
}
