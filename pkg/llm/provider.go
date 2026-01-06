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

	// ExtractToolUsage extracts tool usage events from HTTP events.
	// Accepts *event.HttpRequestEvent (for tool results), *event.HttpResponseEvent (for tool invocations),
	// or *event.SSEEvent (for streaming tool invocations).
	// For SSE, accumulates tool_use blocks across content_block_start/delta/stop events.
	// Returns completed tool events, or nil if none found.
	ExtractToolUsage(e event.Event) []*event.ToolUsageEvent
}
