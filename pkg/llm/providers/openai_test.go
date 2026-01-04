package providers

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIParser_ParseRequest(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name            string
		payload         string
		expectedModel   string
		expectedContent string
		wantErr         bool
	}{
		{
			name: "simple string content",
			payload: `{
				"model": "gpt-4o",
				"messages": [{"role": "user", "content": "Hello, world!"}]
			}`,
			expectedModel:   "gpt-4o",
			expectedContent: "Hello, world!",
		},
		{
			name: "array content blocks (vision)",
			payload: `{
				"model": "gpt-4o",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "text", "text": "What's in this image?"},
						{"type": "image_url", "image_url": {"url": "https://example.com/image.jpg"}}
					]
				}]
			}`,
			expectedModel:   "gpt-4o",
			expectedContent: "What's in this image?",
		},
		{
			name: "multiple messages extracts last user message",
			payload: `{
				"model": "gpt-4o-mini",
				"messages": [
					{"role": "user", "content": "First question"},
					{"role": "assistant", "content": "First answer"},
					{"role": "user", "content": "Follow-up question"}
				]
			}`,
			expectedModel:   "gpt-4o-mini",
			expectedContent: "Follow-up question",
		},
		{
			name: "system and user messages",
			payload: `{
				"model": "gpt-4o",
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "What is 2+2?"}
				]
			}`,
			expectedModel:   "gpt-4o",
			expectedContent: "What is 2+2?",
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json`,
			wantErr: true,
		},
		{
			name: "empty messages array",
			payload: `{
				"model": "gpt-4o",
				"messages": []
			}`,
			expectedModel:   "gpt-4o",
			expectedContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &event.HttpRequestEvent{
				EventHeader:    makeEventHeader(1234, "python"),
				SSLContext:     99999,
				Host:           "api.openai.com",
				Path:           "/v1/chat/completions",
				RequestPayload: []byte(tt.payload),
			}

			result, err := parser.ParseRequest(req)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, event.LLMMessageTypeRequest, result.MessageType)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, uint32(1234), result.PID)
			assert.Equal(t, "python", result.Comm)
			assert.Equal(t, "api.openai.com", result.Host)
			assert.Equal(t, "/v1/chat/completions", result.Path)
			assert.Equal(t, uint64(99999), result.SessionID)
		})
	}
}

func TestOpenAIParser_ParseResponse(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name            string
		payload         string
		expectedModel   string
		expectedContent string
		expectedError   string
		wantErr         bool
	}{
		{
			name: "successful response",
			payload: `{
				"id": "chatcmpl-123",
				"model": "gpt-4o-2024-05-13",
				"choices": [{
					"index": 0,
					"message": {"role": "assistant", "content": "Hello! How can I help you?"},
					"finish_reason": "stop"
				}]
			}`,
			expectedModel:   "gpt-4o-2024-05-13",
			expectedContent: "Hello! How can I help you?",
		},
		{
			name: "error response",
			payload: `{
				"error": {
					"message": "Invalid API key",
					"type": "invalid_request_error",
					"code": "invalid_api_key"
				}
			}`,
			expectedError: "Invalid API key",
		},
		{
			name: "rate limit error",
			payload: `{
				"error": {
					"message": "Rate limit exceeded",
					"type": "rate_limit_error"
				}
			}`,
			expectedError: "Rate limit exceeded",
		},
		{
			name:    "invalid JSON",
			payload: `not json`,
			wantErr: true,
		},
		{
			name: "empty choices array",
			payload: `{
				"id": "chatcmpl-123",
				"model": "gpt-4o",
				"choices": []
			}`,
			expectedModel:   "gpt-4o",
			expectedContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &event.HttpResponseEvent{
				EventHeader: makeEventHeader(5678, "curl"),
				HttpRequestEvent: event.HttpRequestEvent{
					Host: "api.openai.com",
					Path: "/v1/chat/completions",
				},
				SSLContext:      88888,
				ResponsePayload: []byte(tt.payload),
			}

			result, err := parser.ParseResponse(resp)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, event.LLMMessageTypeResponse, result.MessageType)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, tt.expectedError, result.Error)
			assert.Equal(t, uint32(5678), result.PID)
			assert.Equal(t, "curl", result.Comm)
			assert.Equal(t, uint64(88888), result.SessionID)
		})
	}
}

func TestOpenAIParser_ParseStreamEvent(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name            string
		data            string
		expectedModel   string
		expectedContent string
		expectedError   string
		expectedDone    bool
		wantErr         bool
	}{
		{
			name:          "stream chunk with content",
			data:          `{"id":"chatcmpl-123","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			expectedModel: "gpt-4o",
			expectedContent: "Hello",
			expectedDone:  false,
		},
		{
			name:            "stream chunk with role only",
			data:            `{"id":"chatcmpl-123","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			expectedModel:   "gpt-4o",
			expectedContent: "",
			expectedDone:    false,
		},
		{
			name:         "stream chunk with finish_reason",
			data:         `{"id":"chatcmpl-123","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			expectedModel: "gpt-4o",
			expectedDone: true,
		},
		{
			name:         "[DONE] marker",
			data:         "[DONE]",
			expectedDone: true,
		},
		{
			name:          "error in stream",
			data:          `{"error":{"message":"Server error","type":"server_error"}}`,
			expectedError: "Server error",
			expectedDone:  true,
		},
		{
			name:         "empty data",
			data:         "",
			expectedDone: false,
		},
		{
			name:         "whitespace only data",
			data:         "   \n\t  ",
			expectedDone: false,
		},
		{
			name:    "invalid JSON",
			data:    `{broken`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := &event.SSEEvent{
				EventHeader: makeEventHeader(9999, "node"),
				HttpRequestEvent: event.HttpRequestEvent{
					Host: "api.openai.com",
					Path: "/v1/chat/completions",
				},
				SSLContext: 12345,
				Data:       []byte(tt.data),
			}

			result, done, err := parser.ParseStreamEvent(sse)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedDone, done)

			// For empty/whitespace data or [DONE], result may be nil
			if tt.data == "" || tt.data == "   \n\t  " || tt.data == "[DONE]" {
				if tt.data == "[DONE]" {
					assert.Nil(t, result)
				}
				return
			}

			require.NotNil(t, result)
			assert.Equal(t, event.LLMMessageTypeStreamChunk, result.MessageType)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, tt.expectedError, result.Error)
			assert.Equal(t, uint32(9999), result.PID)
			assert.Equal(t, "node", result.Comm)
			assert.Equal(t, uint64(12345), result.SessionID)
		})
	}
}

func TestExtractOpenAIUserPrompt(t *testing.T) {
	tests := []struct {
		name     string
		messages []openaiMessage
		expected string
	}{
		{
			name:     "empty messages",
			messages: []openaiMessage{},
			expected: "",
		},
		{
			name: "single user message",
			messages: []openaiMessage{
				{Role: "user", Content: "Hello"},
			},
			expected: "Hello",
		},
		{
			name: "user and assistant messages",
			messages: []openaiMessage{
				{Role: "user", Content: "Question 1"},
				{Role: "assistant", Content: "Answer 1"},
				{Role: "user", Content: "Question 2"},
			},
			expected: "Question 2",
		},
		{
			name: "only assistant message",
			messages: []openaiMessage{
				{Role: "assistant", Content: "I can help"},
			},
			expected: "",
		},
		{
			name: "nil content",
			messages: []openaiMessage{
				{Role: "user", Content: nil},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractOpenAIUserPrompt(tt.messages)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractOpenAIMessageContent(t *testing.T) {
	tests := []struct {
		name     string
		content  interface{}
		expected string
	}{
		{
			name:     "nil content",
			content:  nil,
			expected: "",
		},
		{
			name:     "string content",
			content:  "Simple text",
			expected: "Simple text",
		},
		{
			name: "single text block",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "Block text"},
			},
			expected: "Block text",
		},
		{
			name: "multiple text blocks",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "First"},
				map[string]interface{}{"type": "text", "text": "Second"},
			},
			expected: "First\nSecond",
		},
		{
			name: "mixed block types (vision)",
			content: []interface{}{
				map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "data"}},
				map[string]interface{}{"type": "text", "text": "Description"},
			},
			expected: "Description",
		},
		{
			name:     "unsupported type",
			content:  12345,
			expected: "",
		},
		{
			name:     "empty array",
			content:  []interface{}{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractOpenAIMessageContent(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractOpenAIResponseText(t *testing.T) {
	tests := []struct {
		name     string
		choices  []openaiChoice
		expected string
	}{
		{
			name:     "empty choices",
			choices:  []openaiChoice{},
			expected: "",
		},
		{
			name: "single choice with message",
			choices: []openaiChoice{
				{Message: &openaiMessage{Role: "assistant", Content: "Hello"}},
			},
			expected: "Hello",
		},
		{
			name: "choice with nil message",
			choices: []openaiChoice{
				{Message: nil},
			},
			expected: "",
		},
		{
			name: "multiple choices uses first",
			choices: []openaiChoice{
				{Message: &openaiMessage{Role: "assistant", Content: "First choice"}},
				{Message: &openaiMessage{Role: "assistant", Content: "Second choice"}},
			},
			expected: "First choice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractOpenAIResponseText(tt.choices)
			assert.Equal(t, tt.expected, result)
		})
	}
}
