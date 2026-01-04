package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// ToolUsageType represents whether this is an invocation or result
type ToolUsageType string

const (
	// ToolUsageTypeInvocation indicates LLM requested a tool call
	ToolUsageTypeInvocation ToolUsageType = "invocation"
	// ToolUsageTypeResult indicates tool result was returned to LLM
	ToolUsageTypeResult ToolUsageType = "result"
)

// ToolUsageEvent represents a tool usage interaction (invocation or result)
// This is provider-agnostic and works for Anthropic, Gemini, OpenAI, etc.
type ToolUsageEvent struct {
	SessionID uint64        `json:"session_id"`
	Timestamp time.Time     `json:"timestamp"`
	UsageType ToolUsageType `json:"usage_type"`
	ToolID    string        `json:"tool_id,omitempty"`
	ToolName  string        `json:"tool_name"`
	Input     string        `json:"input,omitempty"`
	Output    string        `json:"output,omitempty"`
	IsError   bool          `json:"is_error,omitempty"`

	// Process context (from HTTP request/response)
	PID  uint32 `json:"pid"`
	Comm string `json:"comm"`
	Host string `json:"host"`
}

func (e *ToolUsageEvent) Type() EventType { return EventTypeToolUsage }

func (e *ToolUsageEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"session_id": e.SessionID,
		"usage_type": e.UsageType,
		"tool_id":    e.ToolID,
		"tool_name":  e.ToolName,
		"is_error":   e.IsError,
	}
}
