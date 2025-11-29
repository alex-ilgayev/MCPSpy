package providers

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// GeminiParser parses Google Gemini API requests and responses
type GeminiParser struct{}

func NewGeminiParser() *GeminiParser {
	return &GeminiParser{}
}

// Gemini generateContent Request Structure
type geminiRequest struct {
	Contents          []geminiContent      `json:"contents"`
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	Tools             []geminiTool         `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig    `json:"toolConfig,omitempty"`
	SafetySettings    []geminiSafetySetting `json:"safetySettings,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string                 `json:"text,omitempty"`
	InlineData   *geminiInlineData      `json:"inlineData,omitempty"`
	FunctionCall *geminiFunctionCall    `json:"functionCall,omitempty"`
	FunctionResp *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations,omitempty"`
	CodeExecution        *geminiCodeExecution `json:"codeExecution,omitempty"`
}

type geminiFunctionDecl struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type geminiCodeExecution struct{}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type geminiGenerationConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
}

// Gemini generateContent Response Structure
type geminiResponse struct {
	Candidates     []geminiCandidate    `json:"candidates"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion   string               `json:"modelVersion,omitempty"`
	Error          *geminiError         `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content       *geminiContent        `json:"content,omitempty"`
	FinishReason  string                `json:"finishReason,omitempty"`
	Index         int                   `json:"index"`
	SafetyRatings []geminiSafetyRating  `json:"safetyRatings,omitempty"`
}

type geminiPromptFeedback struct {
	SafetyRatings []geminiSafetyRating `json:"safetyRatings,omitempty"`
	BlockReason   string               `json:"blockReason,omitempty"`
}

type geminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Model extraction regex
var geminiModelRegex = regexp.MustCompile(`/models/([^/:]+)`)

// ParseRequest parses a Gemini API request
func (p *GeminiParser) ParseRequest(body []byte, transport event.LLMTransport, endpoint string) (*event.LLMEvent, error) {
	var req geminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// Extract model from endpoint
	model := extractGeminiModel(endpoint)

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderGemini,
		Transport:   transport,
		MessageType: event.LLMMessageTypeRequest,
		Model:       model,
		Endpoint:    endpoint,
		IsStreaming: strings.Contains(endpoint, "streamGenerateContent"),
		Messages:    convertGeminiContents(req.Contents, req.SystemInstruction),
		Raw:         string(body),
	}

	return llmEvent, nil
}

// ParseResponse parses a Gemini API response (non-streaming)
func (p *GeminiParser) ParseResponse(body []byte, transport event.LLMTransport, endpoint string, statusCode int) (*event.LLMEvent, error) {
	var resp geminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	model := extractGeminiModel(endpoint)
	if resp.ModelVersion != "" {
		model = resp.ModelVersion
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderGemini,
		Transport:   transport,
		MessageType: event.LLMMessageTypeResponse,
		Model:       model,
		Endpoint:    endpoint,
		IsStreaming: false,
		StatusCode:  statusCode,
		Raw:         string(body),
	}

	// Handle error response
	if resp.Error != nil {
		llmEvent.Error = &event.LLMError{
			Code:    string(rune(resp.Error.Code)),
			Message: resp.Error.Message,
		}
		return llmEvent, nil
	}

	// Extract response content
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			llmEvent.Messages, llmEvent.ToolCalls = extractGeminiContent(candidate.Content)
		}
		llmEvent.FinishReason = candidate.FinishReason
	}

	// Extract usage
	if resp.UsageMetadata != nil {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  resp.UsageMetadata.TotalTokenCount,
		}
	}

	return llmEvent, nil
}

// ParseStreamChunk parses a Gemini streaming chunk
func (p *GeminiParser) ParseStreamChunk(data string, transport event.LLMTransport, endpoint string, chunkIndex int) (*event.LLMEvent, error) {
	var resp geminiResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}

	model := extractGeminiModel(endpoint)
	if resp.ModelVersion != "" {
		model = resp.ModelVersion
	}

	llmEvent := &event.LLMEvent{
		Timestamp:   time.Now(),
		Provider:    event.ProviderGemini,
		Transport:   transport,
		MessageType: event.LLMMessageTypeStreamChunk,
		Model:       model,
		Endpoint:    endpoint,
		IsStreaming: true,
		ChunkIndex:  chunkIndex,
		Raw:         data,
	}

	// Extract content from candidate
	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		if candidate.Content != nil {
			// Extract text content as chunk
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					llmEvent.ChunkContent = part.Text
					break
				}
			}
			// Also check for function calls
			_, llmEvent.ToolCalls = extractGeminiContent(candidate.Content)
		}
		llmEvent.FinishReason = candidate.FinishReason

		// Check if this is the end
		if candidate.FinishReason != "" {
			llmEvent.MessageType = event.LLMMessageTypeStreamEnd
		}
	}

	// Extract usage
	if resp.UsageMetadata != nil {
		llmEvent.Usage = &event.LLMUsage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  resp.UsageMetadata.TotalTokenCount,
		}
	}

	return llmEvent, nil
}

// extractGeminiModel extracts the model name from the endpoint path
func extractGeminiModel(endpoint string) string {
	matches := geminiModelRegex.FindStringSubmatch(endpoint)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// convertGeminiContents converts Gemini contents to LLMMessage format
func convertGeminiContents(contents []geminiContent, systemInstruction *geminiContent) []event.LLMMessage {
	result := make([]event.LLMMessage, 0, len(contents)+1)

	// Add system instruction if present
	if systemInstruction != nil {
		text := extractGeminiTextFromParts(systemInstruction.Parts)
		if text != "" {
			result = append(result, event.LLMMessage{
				Role:    "system",
				Content: text,
			})
		}
	}

	for _, content := range contents {
		role := content.Role
		// Map Gemini roles to standard roles
		if role == "model" {
			role = "assistant"
		}

		text := extractGeminiTextFromParts(content.Parts)
		result = append(result, event.LLMMessage{
			Role:    role,
			Content: text,
		})
	}

	return result
}

// extractGeminiTextFromParts extracts text from Gemini parts
func extractGeminiTextFromParts(parts []geminiPart) string {
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// extractGeminiContent extracts messages and tool calls from Gemini content
func extractGeminiContent(content *geminiContent) ([]event.LLMMessage, []event.LLMToolCall) {
	var textParts []string
	var toolCalls []event.LLMToolCall

	for _, part := range content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			toolCalls = append(toolCalls, event.LLMToolCall{
				Type:      "function",
				Name:      part.FunctionCall.Name,
				Arguments: string(args),
			})
		}
	}

	role := content.Role
	if role == "model" {
		role = "assistant"
	}

	var messages []event.LLMMessage
	if len(textParts) > 0 {
		messages = []event.LLMMessage{
			{
				Role:    role,
				Content: strings.Join(textParts, "\n"),
			},
		}
	}

	return messages, toolCalls
}
