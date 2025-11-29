package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// LLMMessageType represents the type of LLM message
type LLMMessageType string

const (
	LLMMessageTypeRequest  LLMMessageType = "request"
	LLMMessageTypeResponse LLMMessageType = "response"
)

// LLMEvent represents a parsed Anthropic API message
type LLMEvent struct {
	Timestamp   time.Time      `json:"timestamp"`
	MessageType LLMMessageType `json:"message_type"`
	PID         uint32         `json:"pid"`
	Comm        string         `json:"comm"`
	Model       string         `json:"model,omitempty"`
	Content     string         `json:"content,omitempty"` // User prompt (request) or assistant response (response)
	Error       string         `json:"error,omitempty"`
}

func (e *LLMEvent) Type() EventType { return EventTypeLLMMessage }

func (e *LLMEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"message_type": e.MessageType,
		"model":        e.Model,
		"pid":          e.PID,
		"comm":         e.Comm,
	}
}
