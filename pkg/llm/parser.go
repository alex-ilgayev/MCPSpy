package llm

import (
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/llm/providers"
	"github.com/sirupsen/logrus"
)

// Parser handles parsing of LLM API messages
type Parser struct {
	eventBus  bus.EventBus
	providers map[Provider]ProviderParser
}

// NewParser creates a new LLM parser
func NewParser(eventBus bus.EventBus) (*Parser, error) {
	p := &Parser{
		eventBus:  eventBus,
		providers: make(map[Provider]ProviderParser),
	}

	// Register providers
	p.providers[ProviderAnthropic] = providers.NewAnthropicParser()

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

	p.eventBus.Publish(llmEvent)
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

	p.eventBus.Publish(llmEvent)
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

	if llmEvent != nil && llmEvent.Content != "" {
		p.eventBus.Publish(llmEvent)
	}
}

func (p *Parser) Close() {
	p.eventBus.Unsubscribe(event.EventTypeHttpRequest, p.handleRequest)
	p.eventBus.Unsubscribe(event.EventTypeHttpResponse, p.handleResponse)
	p.eventBus.Unsubscribe(event.EventTypeHttpSSE, p.handleSSE)
}
