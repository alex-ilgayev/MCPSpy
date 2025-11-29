package llm

import (
	"strings"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/llm/providers"
	"github.com/sirupsen/logrus"
)

// Parser handles parsing of Anthropic API messages
type Parser struct {
	eventBus bus.EventBus
	parser   *providers.AnthropicParser

	// Stream sessions for collecting streaming responses
	streamSessions map[uint64]*streamSession
	streamMu       sync.Mutex
}

type streamSession struct {
	pid          uint32
	comm         string
	model        string
	chunkIndex   int
	content      strings.Builder
	toolCalls    []event.LLMToolCall
	inputTokens  int
	outputTokens int
}

// NewParser creates a new LLM parser
func NewParser(eventBus bus.EventBus) (*Parser, error) {
	p := &Parser{
		eventBus:       eventBus,
		parser:         providers.NewAnthropicParser(),
		streamSessions: make(map[uint64]*streamSession),
	}

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

	logrus.Info("LLM parser initialized (Anthropic only)")
	return p, nil
}

func (p *Parser) handleRequest(e event.Event) {
	httpEvent, ok := e.(*event.HttpRequestEvent)
	if !ok {
		return
	}

	if !IsAnthropicRequest(httpEvent.Host, httpEvent.Path) {
		return
	}

	llmEvent, err := p.parser.ParseRequest(httpEvent.RequestPayload, httpEvent.PID, httpEvent.Comm())
	if err != nil {
		logrus.WithError(err).Debug("Failed to parse Anthropic request")
		return
	}

	if llmEvent.IsStreaming {
		p.startStreamSession(httpEvent.SSLContext, llmEvent)
	}

	logrus.WithFields(llmEvent.LogFields()).Debug("Anthropic request parsed")
	p.eventBus.Publish(llmEvent)
}

func (p *Parser) handleResponse(e event.Event) {
	httpEvent, ok := e.(*event.HttpResponseEvent)
	if !ok {
		return
	}

	if !IsAnthropicRequest(httpEvent.Host, httpEvent.Path) {
		return
	}

	llmEvent, err := p.parser.ParseResponse(httpEvent.ResponsePayload, httpEvent.PID, httpEvent.Comm())
	if err != nil {
		logrus.WithError(err).Debug("Failed to parse Anthropic response")
		return
	}

	logrus.WithFields(llmEvent.LogFields()).Debug("Anthropic response parsed")
	p.eventBus.Publish(llmEvent)
}

func (p *Parser) handleSSE(e event.Event) {
	sseEvent, ok := e.(*event.SSEEvent)
	if !ok {
		return
	}

	if !IsAnthropicRequest(sseEvent.Host, sseEvent.Path) {
		return
	}

	session := p.getOrCreateStreamSession(sseEvent.SSLContext, sseEvent.PID, sseEvent.Comm())
	session.chunkIndex++

	data := strings.TrimSpace(string(sseEvent.Data))
	if data == "" {
		return
	}

	llmEvent, err := p.parser.ParseStreamEvent(data, sseEvent.PID, sseEvent.Comm())
	if err != nil {
		logrus.WithError(err).Debug("Failed to parse Anthropic SSE")
		return
	}

	if llmEvent == nil {
		return // ping or other ignored event
	}

	// Aggregate content
	if llmEvent.ChunkContent != "" {
		session.content.WriteString(llmEvent.ChunkContent)
	}
	if len(llmEvent.ToolCalls) > 0 {
		session.toolCalls = append(session.toolCalls, llmEvent.ToolCalls...)
	}
	if llmEvent.Model != "" {
		session.model = llmEvent.Model
	}
	if llmEvent.InputTokens > 0 {
		session.inputTokens = llmEvent.InputTokens
	}
	if llmEvent.OutputTokens > 0 {
		session.outputTokens = llmEvent.OutputTokens
	}

	p.eventBus.Publish(llmEvent)

	// If stream ended, emit aggregated response
	if llmEvent.MessageType == event.LLMMessageTypeStreamEnd {
		p.emitAggregatedResponse(sseEvent.SSLContext, session, llmEvent)
	}
}

func (p *Parser) startStreamSession(sslContext uint64, request *event.LLMEvent) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	p.streamSessions[sslContext] = &streamSession{
		pid:   request.PID,
		comm:  request.Comm,
		model: request.Model,
	}
}

func (p *Parser) getOrCreateStreamSession(sslContext uint64, pid uint32, comm string) *streamSession {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	session, exists := p.streamSessions[sslContext]
	if !exists {
		session = &streamSession{pid: pid, comm: comm}
		p.streamSessions[sslContext] = session
	}
	return session
}

func (p *Parser) emitAggregatedResponse(sslContext uint64, session *streamSession, finalEvent *event.LLMEvent) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()

	aggregated := &event.LLMEvent{
		Timestamp:    finalEvent.Timestamp,
		MessageType:  event.LLMMessageTypeResponse,
		PID:          session.pid,
		Comm:         session.comm,
		Model:        session.model,
		IsStreaming:  true,
		StopReason:   finalEvent.StopReason,
		InputTokens:  session.inputTokens,
		OutputTokens: session.outputTokens,
		ToolCalls:    session.toolCalls,
	}

	if session.content.Len() > 0 {
		aggregated.Messages = []event.LLMMessage{{
			Role:    "assistant",
			Content: session.content.String(),
		}}
	}

	logrus.WithFields(aggregated.LogFields()).Debug("Anthropic aggregated response")
	p.eventBus.Publish(aggregated)

	delete(p.streamSessions, sslContext)
}

func (p *Parser) Close() {
	p.eventBus.Unsubscribe(event.EventTypeHttpRequest, p.handleRequest)
	p.eventBus.Unsubscribe(event.EventTypeHttpResponse, p.handleResponse)
	p.eventBus.Unsubscribe(event.EventTypeHttpSSE, p.handleSSE)

	p.streamMu.Lock()
	p.streamSessions = make(map[uint64]*streamSession)
	p.streamMu.Unlock()
}
