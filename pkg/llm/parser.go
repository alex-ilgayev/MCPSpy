package llm

import (
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/llm/providers"
	"github.com/sirupsen/logrus"
)

// ParserConfig controls which events the parser publishes
type ParserConfig struct {
	PublishLLMEvents  bool // Publish LLM request/response/stream events
	PublishToolEvents bool // Publish tool usage events
}

// Parser handles parsing of LLM API messages
type Parser struct {
	eventBus  bus.EventBus
	providers map[Provider]ProviderParser
	config    ParserConfig
}

// NewParser creates a new LLM parser with default config (publish all events)
func NewParser(eventBus bus.EventBus) (*Parser, error) {
	return NewParserWithConfig(eventBus, ParserConfig{
		PublishLLMEvents:  true,
		PublishToolEvents: true,
	})
}

// NewParserWithConfig creates a new LLM parser with custom config
func NewParserWithConfig(eventBus bus.EventBus, config ParserConfig) (*Parser, error) {
	p := &Parser{
		eventBus:  eventBus,
		providers: make(map[Provider]ProviderParser),
		config:    config,
	}

	// Register providers
	p.providers[ProviderAnthropic] = providers.NewAnthropicParser()
	p.providers[ProviderGemini] = providers.NewGeminiParser()

	if err := p.eventBus.Subscribe(event.EventTypeHttpRequest, p.handleRequest); err != nil {
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpResponse, p.handleResponse); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.eventBus.Subscribe(event.EventTypeHttpSSE, p.handleSSE); err != nil {
		p.Close()
		return nil, err
	}

	logrus.Debug("LLM parser initialized")
	return p, nil
}

func (p *Parser) handleRequest(e event.Event) {
	httpEvent, ok := e.(*event.HttpRequestEvent)
	if !ok {
		return
	}

	provider := DetectProvider(httpEvent.Host, httpEvent.Path)
	if provider == ProviderUnknown {
		return
	}

	parser, ok := p.providers[provider]
	if !ok {
		return
	}

	llmEvent, err := parser.ParseRequest(httpEvent)
	if err != nil {
		logrus.WithError(err).Warn("Failed to parse LLM request")
		return
	}

	if p.config.PublishLLMEvents {
		p.eventBus.Publish(llmEvent)
	}

	// Extract and publish tool results from request
	if p.config.PublishToolEvents {
		toolEvents := parser.ExtractToolUsage(httpEvent.RequestPayload, httpEvent.SSLContext, true)
		for _, te := range toolEvents {
			// Add process context from HTTP event
			te.PID = httpEvent.PID
			te.Comm = httpEvent.Comm()
			te.Host = httpEvent.Host
			p.eventBus.Publish(te)
		}
	}
}

func (p *Parser) handleResponse(e event.Event) {
	httpEvent, ok := e.(*event.HttpResponseEvent)
	if !ok {
		return
	}

	provider := DetectProvider(httpEvent.Host, httpEvent.Path)
	if provider == ProviderUnknown {
		return
	}

	// Skip streaming responses - they're handled by SSE events
	if contentType, ok := httpEvent.ResponseHeaders["Content-Type"]; ok {
		if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
			return
		}
	}

	parser, ok := p.providers[provider]
	if !ok {
		return
	}

	llmEvent, err := parser.ParseResponse(httpEvent)
	if err != nil {
		logrus.WithError(err).Warn("Failed to parse LLM response")
		return
	}

	if p.config.PublishLLMEvents {
		p.eventBus.Publish(llmEvent)
	}

	// Extract and publish tool invocations from response
	if p.config.PublishToolEvents {
		toolEvents := parser.ExtractToolUsage(httpEvent.ResponsePayload, httpEvent.SSLContext, false)
		for _, te := range toolEvents {
			// Add process context from HTTP event
			te.PID = httpEvent.PID
			te.Comm = httpEvent.Comm()
			te.Host = httpEvent.Host
			p.eventBus.Publish(te)
		}
	}
}

func (p *Parser) handleSSE(e event.Event) {
	sseEvent, ok := e.(*event.SSEEvent)
	if !ok {
		return
	}

	provider := DetectProvider(sseEvent.Host, sseEvent.Path)
	if provider == ProviderUnknown {
		return
	}

	parser, ok := p.providers[provider]
	if !ok {
		return
	}

	llmEvent, _, err := parser.ParseStreamEvent(sseEvent)
	if err != nil {
		logrus.WithError(err).Warn("Failed to parse LLM SSE")
		return
	}

	if p.config.PublishLLMEvents && llmEvent != nil && llmEvent.Content != "" {
		p.eventBus.Publish(llmEvent)
	}

	// Extract and publish tool invocations from streaming SSE
	if p.config.PublishToolEvents {
		toolEvents := parser.ExtractToolUsageFromSSE(sseEvent)
		for _, te := range toolEvents {
			// Add process context from SSE event
			te.PID = sseEvent.PID
			te.Comm = sseEvent.Comm()
			te.Host = sseEvent.Host
			p.eventBus.Publish(te)
		}
	}
}

func (p *Parser) Close() {
	p.eventBus.Unsubscribe(event.EventTypeHttpRequest, p.handleRequest)
	p.eventBus.Unsubscribe(event.EventTypeHttpResponse, p.handleResponse)
	p.eventBus.Unsubscribe(event.EventTypeHttpSSE, p.handleSSE)
}
