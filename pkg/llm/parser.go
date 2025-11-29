package llm

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/llm/providers"
	"github.com/sirupsen/logrus"
)

// Parser handles parsing of LLM API messages
// Subscribes to the following events:
// - EventTypeHttpRequest
// - EventTypeHttpResponse
// - EventTypeHttpSSE
//
// Emits the following events:
// - EventTypeLLMMessage
type Parser struct {
	eventBus         bus.EventBus
	enabledProviders []string

	// Provider parsers
	openaiParser    *providers.OpenAIParser
	anthropicParser *providers.AnthropicParser
	geminiParser    *providers.GeminiParser
	ollamaParser    *providers.OllamaParser

	// Stream aggregators for collecting streaming responses
	// Key: SSL context or session ID
	streamSessions map[uint64]*streamSession
	streamMu       sync.Mutex
}

// streamSession tracks streaming response state
type streamSession struct {
	provider    event.LLMProvider
	transport   event.LLMTransport
	endpoint    string
	model       string
	requestID   string
	chunkIndex  int
	content     strings.Builder
	toolCalls   []event.LLMToolCall
	usage       *event.LLMUsage
}

// NewParser creates a new LLM parser
func NewParser(eventBus bus.EventBus, enabledProviders []string) (*Parser, error) {
	p := &Parser{
		eventBus:         eventBus,
		enabledProviders: ValidateProviders(enabledProviders),
		openaiParser:     providers.NewOpenAIParser(),
		anthropicParser:  providers.NewAnthropicParser(),
		geminiParser:     providers.NewGeminiParser(),
		ollamaParser:     providers.NewOllamaParser(),
		streamSessions:   make(map[uint64]*streamSession),
	}

	if err := p.eventBus.Subscribe(event.EventTypeHttpRequest, p.ParseHttpRequest); err != nil {
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpResponse, p.ParseHttpResponse); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpSSE, p.ParseSSE); err != nil {
		p.Close()
		return nil, err
	}

	logrus.WithField("providers", p.enabledProviders).Info("LLM parser initialized")
	return p, nil
}

// ParseHttpRequest parses LLM API requests
func (p *Parser) ParseHttpRequest(e event.Event) {
	httpEvent, ok := e.(*event.HttpRequestEvent)
	if !ok {
		return
	}

	// Check if this is an LLM API request
	provider, detected := DetectProvider(httpEvent.Host, httpEvent.Path, p.enabledProviders)
	if !detected {
		return
	}

	logrus.WithFields(logrus.Fields{
		"provider": provider,
		"host":     httpEvent.Host,
		"path":     httpEvent.Path,
	}).Debug("Detected LLM API request")

	transport := event.LLMTransport{
		PID:  httpEvent.PID,
		Comm: httpEvent.Comm(),
		Host: httpEvent.Host,
	}

	var llmEvent *event.LLMEvent
	var err error

	switch provider {
	case event.ProviderOpenAI, event.ProviderAzure:
		llmEvent, err = p.openaiParser.ParseRequest(httpEvent.RequestPayload, transport, httpEvent.Path)
		if llmEvent != nil {
			llmEvent.Provider = provider // Override for Azure
		}
	case event.ProviderAnthropic:
		llmEvent, err = p.anthropicParser.ParseRequest(httpEvent.RequestPayload, transport, httpEvent.Path)
	case event.ProviderGemini:
		llmEvent, err = p.geminiParser.ParseRequest(httpEvent.RequestPayload, transport, httpEvent.Path)
	case event.ProviderOllama:
		llmEvent, err = p.ollamaParser.ParseRequest(httpEvent.RequestPayload, transport, httpEvent.Path)
	}

	if err != nil {
		logrus.WithError(err).WithField("provider", provider).Debug("Failed to parse LLM request")
		return
	}

	if llmEvent != nil {
		// Check if streaming and track session
		if llmEvent.IsStreaming || IsStreamingEndpoint(provider, httpEvent.Path) {
			p.startStreamSession(httpEvent.SSLContext, llmEvent)
		}

		logrus.WithFields(llmEvent.LogFields()).Debug("LLM request parsed")
		p.eventBus.Publish(llmEvent)
	}
}

// ParseHttpResponse parses LLM API responses (non-streaming)
func (p *Parser) ParseHttpResponse(e event.Event) {
	httpEvent, ok := e.(*event.HttpResponseEvent)
	if !ok {
		return
	}

	// Check if this is an LLM API response
	provider, detected := DetectProvider(httpEvent.Host, httpEvent.Path, p.enabledProviders)
	if !detected {
		return
	}

	logrus.WithFields(logrus.Fields{
		"provider": provider,
		"host":     httpEvent.Host,
		"path":     httpEvent.Path,
		"code":     httpEvent.Code,
	}).Debug("Detected LLM API response")

	transport := event.LLMTransport{
		PID:  httpEvent.PID,
		Comm: httpEvent.Comm(),
		Host: httpEvent.Host,
	}

	var llmEvent *event.LLMEvent
	var err error

	switch provider {
	case event.ProviderOpenAI, event.ProviderAzure:
		llmEvent, err = p.openaiParser.ParseResponse(httpEvent.ResponsePayload, transport, httpEvent.Path, httpEvent.Code)
		if llmEvent != nil {
			llmEvent.Provider = provider
		}
	case event.ProviderAnthropic:
		llmEvent, err = p.anthropicParser.ParseResponse(httpEvent.ResponsePayload, transport, httpEvent.Path, httpEvent.Code)
	case event.ProviderGemini:
		llmEvent, err = p.geminiParser.ParseResponse(httpEvent.ResponsePayload, transport, httpEvent.Path, httpEvent.Code)
	case event.ProviderOllama:
		llmEvent, err = p.ollamaParser.ParseResponse(httpEvent.ResponsePayload, transport, httpEvent.Path, httpEvent.Code)
	}

	if err != nil {
		logrus.WithError(err).WithField("provider", provider).Debug("Failed to parse LLM response")
		return
	}

	if llmEvent != nil {
		logrus.WithFields(llmEvent.LogFields()).Debug("LLM response parsed")
		p.eventBus.Publish(llmEvent)
	}
}

// ParseSSE parses Server-Sent Events for streaming LLM responses
func (p *Parser) ParseSSE(e event.Event) {
	sseEvent, ok := e.(*event.SSEEvent)
	if !ok {
		return
	}

	// Check if this is an LLM API SSE
	provider, detected := DetectProvider(sseEvent.Host, sseEvent.Path, p.enabledProviders)
	if !detected {
		return
	}

	transport := event.LLMTransport{
		PID:  sseEvent.PID,
		Comm: sseEvent.Comm(),
		Host: sseEvent.Host,
	}

	// Get or create stream session
	session := p.getOrCreateStreamSession(sseEvent.SSLContext, provider, transport, sseEvent.Path)
	session.chunkIndex++

	// Extract data from SSE event
	data := extractSSEData(sseEvent.Data)
	if data == "" {
		return
	}

	var llmEvent *event.LLMEvent
	var err error

	switch provider {
	case event.ProviderOpenAI, event.ProviderAzure:
		llmEvent, err = p.openaiParser.ParseStreamChunk(data, transport, sseEvent.Path, session.chunkIndex)
		if llmEvent != nil {
			llmEvent.Provider = provider
		}
	case event.ProviderAnthropic:
		// Anthropic SSE includes event type
		eventType := sseEvent.SSEEventType
		llmEvent, err = p.anthropicParser.ParseStreamEvent(eventType, data, transport, sseEvent.Path, session.chunkIndex)
	case event.ProviderGemini:
		llmEvent, err = p.geminiParser.ParseStreamChunk(data, transport, sseEvent.Path, session.chunkIndex)
	case event.ProviderOllama:
		// Ollama uses newline-delimited JSON, not SSE
		llmEvent, err = p.ollamaParser.ParseStreamChunk(data, transport, sseEvent.Path, session.chunkIndex)
	}

	if err != nil {
		logrus.WithError(err).WithField("provider", provider).Debug("Failed to parse LLM SSE chunk")
		return
	}

	if llmEvent != nil {
		// Aggregate streaming content
		if llmEvent.ChunkContent != "" {
			session.content.WriteString(llmEvent.ChunkContent)
		}
		if len(llmEvent.ToolCalls) > 0 {
			session.toolCalls = append(session.toolCalls, llmEvent.ToolCalls...)
		}
		if llmEvent.Usage != nil {
			session.usage = llmEvent.Usage
		}
		if llmEvent.Model != "" {
			session.model = llmEvent.Model
		}
		if llmEvent.RequestID != "" {
			session.requestID = llmEvent.RequestID
		}

		// Emit individual chunk events
		logrus.WithFields(llmEvent.LogFields()).Trace("LLM SSE chunk parsed")
		p.eventBus.Publish(llmEvent)

		// If stream ended, emit aggregated response and clean up
		if llmEvent.MessageType == event.LLMMessageTypeStreamEnd {
			p.emitAggregatedResponse(sseEvent.SSLContext, session, llmEvent)
		}
	}
}

// startStreamSession initializes a stream session for tracking
func (p *Parser) startStreamSession(sslContext uint64, request *event.LLMEvent) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	p.streamSessions[sslContext] = &streamSession{
		provider:  request.Provider,
		transport: request.Transport,
		endpoint:  request.Endpoint,
		model:     request.Model,
	}
}

// getOrCreateStreamSession gets or creates a stream session
func (p *Parser) getOrCreateStreamSession(sslContext uint64, provider event.LLMProvider, transport event.LLMTransport, endpoint string) *streamSession {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	session, exists := p.streamSessions[sslContext]
	if !exists {
		session = &streamSession{
			provider:  provider,
			transport: transport,
			endpoint:  endpoint,
		}
		p.streamSessions[sslContext] = session
	}
	return session
}

// emitAggregatedResponse emits the aggregated streaming response
func (p *Parser) emitAggregatedResponse(sslContext uint64, session *streamSession, finalEvent *event.LLMEvent) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	// Create aggregated response event
	aggregated := &event.LLMEvent{
		Timestamp:    finalEvent.Timestamp,
		Provider:     session.provider,
		Transport:    session.transport,
		MessageType:  event.LLMMessageTypeResponse,
		Model:        session.model,
		Endpoint:     session.endpoint,
		IsStreaming:  true,
		RequestID:    session.requestID,
		FinishReason: finalEvent.FinishReason,
		Usage:        session.usage,
		ToolCalls:    session.toolCalls,
	}

	// Add aggregated content as assistant message
	if session.content.Len() > 0 {
		aggregated.Messages = []event.LLMMessage{
			{
				Role:    "assistant",
				Content: session.content.String(),
			},
		}
	}

	logrus.WithFields(aggregated.LogFields()).Debug("LLM aggregated streaming response")
	p.eventBus.Publish(aggregated)

	// Clean up session
	delete(p.streamSessions, sslContext)
}

// extractSSEData extracts the data field from SSE event
func extractSSEData(data []byte) string {
	// SSE data might have "data: " prefix stripped already by SSE parser
	// but handle both cases
	str := string(data)
	if strings.HasPrefix(str, "data: ") {
		str = strings.TrimPrefix(str, "data: ")
	}
	str = strings.TrimSpace(str)

	// Try to parse as JSON to validate
	var js json.RawMessage
	if err := json.Unmarshal([]byte(str), &js); err != nil {
		// If not valid JSON, return as-is (might be [DONE] or similar)
		return str
	}
	return str
}

// Close cleans up the parser
func (p *Parser) Close() {
	p.eventBus.Unsubscribe(event.EventTypeHttpRequest, p.ParseHttpRequest)
	p.eventBus.Unsubscribe(event.EventTypeHttpResponse, p.ParseHttpResponse)
	p.eventBus.Unsubscribe(event.EventTypeHttpSSE, p.ParseSSE)

	p.streamMu.Lock()
	p.streamSessions = make(map[uint64]*streamSession)
	p.streamMu.Unlock()
}
