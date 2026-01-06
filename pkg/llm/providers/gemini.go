package providers

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// GeminiParser parses Google Gemini API requests and responses
type GeminiParser struct {
	modelPattern *regexp.Regexp
}

func NewGeminiParser() *GeminiParser {
	return &GeminiParser{
		// Match model name from path like /v1beta/models/gemini-2.0-flash:generateContent
		modelPattern: regexp.MustCompile(`/models/([^/:]+)`),
	}
}

// Request structures
type geminiRequest struct {
	Contents          []geminiContent    `json:"contents"`
	GenerationConfig  *geminiGenConfig   `json:"generationConfig,omitempty"`
	SystemInstruction *geminiContent     `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response,omitempty"`
}

type geminiGenConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// Response structures
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates,omitempty"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	ModelVersion  string            `json:"modelVersion,omitempty"`
	Error         *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      *geminiContent `json:"content,omitempty"`
	FinishReason string         `json:"finishReason,omitempty"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount,omitempty"`
}

type geminiError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ParseRequest parses a Gemini API request
func (p *GeminiParser) ParseRequest(req *event.HttpRequestEvent) (*event.LLMEvent, error) {
	var geminiReq geminiRequest
	if err := json.Unmarshal(req.RequestPayload, &geminiReq); err != nil {
		return nil, err
	}

	// Extract model from URL path (e.g., /v1beta/models/gemini-2.0-flash:generateContent)
	model := p.extractModelFromPath(req.Path)

	return &event.LLMEvent{
		SessionID:   req.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeRequest,
		PID:         req.PID,
		Comm:        req.Comm(),
		Host:        req.Host,
		Path:        req.Path,
		Model:       model,
		Content:     extractGeminiUserPrompt(geminiReq.Contents),
		RawJSON:     string(req.RequestPayload),
	}, nil
}

// ParseResponse parses a Gemini API response (non-streaming)
func (p *GeminiParser) ParseResponse(resp *event.HttpResponseEvent) (*event.LLMEvent, error) {
	var geminiResp geminiResponse
	if err := json.Unmarshal(resp.ResponsePayload, &geminiResp); err != nil {
		return nil, err
	}

	// Extract model from URL path (fallback) or use modelVersion from response
	model := geminiResp.ModelVersion
	if model == "" {
		model = p.extractModelFromPath(resp.Path)
	}

	ev := &event.LLMEvent{
		SessionID:   resp.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeResponse,
		PID:         resp.PID,
		Comm:        resp.Comm(),
		Host:        resp.Host,
		Path:        resp.Path,
		Model:       model,
		RawJSON:     string(resp.ResponsePayload),
	}

	// Check for error response
	if geminiResp.Error != nil && geminiResp.Error.Message != "" {
		ev.Error = geminiResp.Error.Message
		return ev, nil
	}

	ev.Content = extractGeminiResponseText(geminiResp.Candidates)
	return ev, nil
}

// ParseStreamEvent parses a Gemini streaming SSE event
// Gemini streaming is simpler than Anthropic - each SSE event contains a complete GenerateContentResponse
// Returns: event (may be nil for skip), done flag, error
func (p *GeminiParser) ParseStreamEvent(sse *event.SSEEvent) (*event.LLMEvent, bool, error) {
	data := strings.TrimSpace(string(sse.Data))
	if data == "" {
		return nil, false, nil
	}

	var streamResp geminiResponse
	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return nil, false, err
	}

	// Extract model from URL path (fallback) or use modelVersion from response
	model := streamResp.ModelVersion
	if model == "" {
		model = p.extractModelFromPath(sse.Path)
	}

	ev := &event.LLMEvent{
		SessionID:   sse.SSLContext,
		Timestamp:   time.Now(),
		MessageType: event.LLMMessageTypeStreamChunk,
		PID:         sse.PID,
		Comm:        sse.Comm(),
		Host:        sse.Host,
		Path:        sse.Path,
		Model:       model,
		RawJSON:     data, // Original SSE JSON payload
	}

	// Check for error
	if streamResp.Error != nil && streamResp.Error.Message != "" {
		ev.Error = streamResp.Error.Message
		return ev, true, nil
	}

	// Extract text from candidates
	ev.Content = extractGeminiResponseText(streamResp.Candidates)

	// Determine if stream is done (finishReason is set to a terminal value)
	done := false
	if len(streamResp.Candidates) > 0 && streamResp.Candidates[0].FinishReason != "" {
		reason := streamResp.Candidates[0].FinishReason
		done = reason == "STOP" || reason == "MAX_TOKENS" || reason == "SAFETY" ||
			reason == "RECITATION" || reason == "OTHER"
	}

	return ev, done, nil
}

// extractModelFromPath extracts the model name from URL path
// e.g., /v1beta/models/gemini-2.0-flash:generateContent -> gemini-2.0-flash
func (p *GeminiParser) extractModelFromPath(path string) string {
	// Remove query string
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	matches := p.modelPattern.FindStringSubmatch(path)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// extractGeminiUserPrompt extracts the user's prompt from the contents array
// Gets the last user message (or first message if no role specified)
func extractGeminiUserPrompt(contents []geminiContent) string {
	// Get the last user message
	for i := len(contents) - 1; i >= 0; i-- {
		// Gemini uses "user" role, or empty role for single-turn
		if contents[i].Role == "user" || contents[i].Role == "" {
			return extractGeminiPartsText(contents[i].Parts)
		}
	}
	return ""
}

// extractGeminiPartsText extracts text from an array of parts
func extractGeminiPartsText(parts []geminiPart) string {
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// extractGeminiResponseText extracts text from candidates array
func extractGeminiResponseText(candidates []geminiCandidate) string {
	if len(candidates) == 0 {
		return ""
	}

	// Use first candidate's content
	if candidates[0].Content != nil {
		return extractGeminiPartsText(candidates[0].Content.Parts)
	}
	return ""
}

// ExtractToolUsage extracts tool usage events from HTTP events.
// Accepts *event.HttpRequestEvent (for function responses), *event.HttpResponseEvent (for function calls),
// or *event.SSEEvent (for streaming function calls).
func (p *GeminiParser) ExtractToolUsage(e event.Event) []*event.ToolUsageEvent {
	switch ev := e.(type) {
	case *event.HttpRequestEvent:
		return p.extractFunctionResponses(ev.RequestPayload, ev.SSLContext)
	case *event.HttpResponseEvent:
		return p.extractFunctionCalls(ev.ResponsePayload, ev.SSLContext)
	case *event.SSEEvent:
		return p.extractToolUsageFromSSE(ev)
	default:
		return nil
	}
}

// extractFunctionCalls extracts functionCall from response candidates
func (p *GeminiParser) extractFunctionCalls(payload []byte, sessionID uint64) []*event.ToolUsageEvent {
	var resp geminiResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil
	}

	var events []*event.ToolUsageEvent
	for _, candidate := range resp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall == nil {
				continue
			}
			events = append(events, &event.ToolUsageEvent{
				SessionID: sessionID,
				Timestamp: time.Now(),
				UsageType: event.ToolUsageTypeInvocation,
				ToolName:  part.FunctionCall.Name,
				Input:     string(part.FunctionCall.Args),
			})
		}
	}

	return events
}

// extractFunctionResponses extracts functionResponse from request contents
func (p *GeminiParser) extractFunctionResponses(payload []byte, sessionID uint64) []*event.ToolUsageEvent {
	var req geminiRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil
	}

	var events []*event.ToolUsageEvent
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse == nil {
				continue
			}
			events = append(events, &event.ToolUsageEvent{
				SessionID: sessionID,
				Timestamp: time.Now(),
				UsageType: event.ToolUsageTypeResult,
				ToolName:  part.FunctionResponse.Name,
				Output:    string(part.FunctionResponse.Response),
			})
		}
	}

	return events
}

// extractToolUsageFromSSE extracts tool usage from streaming SSE events
// For Gemini, function calls in streaming come as complete objects in each chunk,
// so we extract them directly from the SSE data.
func (p *GeminiParser) extractToolUsageFromSSE(sse *event.SSEEvent) []*event.ToolUsageEvent {
	data := strings.TrimSpace(string(sse.Data))
	if data == "" {
		return nil
	}

	var streamResp geminiResponse
	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return nil
	}

	var events []*event.ToolUsageEvent
	for _, candidate := range streamResp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall == nil {
				continue
			}
			events = append(events, &event.ToolUsageEvent{
				SessionID: sse.SSLContext,
				Timestamp: time.Now(),
				UsageType: event.ToolUsageTypeInvocation,
				ToolName:  part.FunctionCall.Name,
				Input:     string(part.FunctionCall.Args),
			})
		}
	}

	return events
}
