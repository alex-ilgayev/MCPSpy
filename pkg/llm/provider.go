package llm

import "github.com/alex-ilgayev/mcpspy/pkg/event"

// ProviderParser defines the interface for LLM provider-specific parsers
type ProviderParser interface {
	// ParseRequest parses an HTTP request and returns an LLM event
	ParseRequest(req *event.HttpRequestEvent) (*event.LLMEvent, error)

	// ParseResponse parses a non-streaming HTTP response and returns an LLM event
	ParseResponse(resp *event.HttpResponseEvent) (*event.LLMEvent, error)

	// ParseStreamEvent parses a single SSE event during streaming
	// Returns: event (may be nil for skip), done flag, error
	ParseStreamEvent(sse *event.SSEEvent) (*event.LLMEvent, bool, error)

	// ExtractToolUsage extracts tool usage events from request or response payload.
	// isRequest=true for tool results (in request), isRequest=false for tool invocations (in response).
	// Returns nil slice if no tool usage found.
	ExtractToolUsage(payload []byte, sessionID uint64, isRequest bool) []*event.ToolUsageEvent

	// ExtractToolUsageFromSSE extracts tool usage from streaming SSE events.
	// Accumulates tool_use blocks across content_block_start/delta/stop events.
	// Returns completed tool events (when content_block_stop is received).
	ExtractToolUsageFromSSE(sse *event.SSEEvent) []*event.ToolUsageEvent
}
