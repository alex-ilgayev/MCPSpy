package providers

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiParser_ParseRequest(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name            string
		path            string
		payload         string
		expectedModel   string
		expectedContent string
		wantErr         bool
	}{
		{
			name: "simple text content",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"contents": [{"role": "user", "parts": [{"text": "Hello, world!"}]}]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Hello, world!",
		},
		{
			name: "multiple parts in content",
			path: "/v1beta/models/gemini-1.5-pro:generateContent",
			payload: `{
				"contents": [{
					"role": "user",
					"parts": [
						{"text": "First paragraph."},
						{"text": "Second paragraph."}
					]
				}]
			}`,
			expectedModel:   "gemini-1.5-pro",
			expectedContent: "First paragraph.\nSecond paragraph.",
		},
		{
			name: "conversation history - extracts last user message",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"contents": [
					{"role": "user", "parts": [{"text": "First question"}]},
					{"role": "model", "parts": [{"text": "First answer"}]},
					{"role": "user", "parts": [{"text": "Follow-up question"}]}
				]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Follow-up question",
		},
		{
			name: "single turn without role",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"contents": [{"parts": [{"text": "What is 2+2?"}]}]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "What is 2+2?",
		},
		{
			name: "model extraction with query params",
			path: "/v1beta/models/gemini-2.0-flash:generateContent?key=AIzaSy...",
			payload: `{
				"contents": [{"role": "user", "parts": [{"text": "Test"}]}]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Test",
		},
		{
			name: "streamGenerateContent endpoint",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
			payload: `{
				"contents": [{"role": "user", "parts": [{"text": "Stream test"}]}]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Stream test",
		},
		{
			name:    "invalid JSON",
			path:    "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{invalid json`,
			wantErr: true,
		},
		{
			name: "empty contents array",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"contents": []
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "",
		},
		{
			name: "content with generationConfig",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"contents": [{"role": "user", "parts": [{"text": "Hello"}]}],
				"generationConfig": {
					"temperature": 0.7,
					"maxOutputTokens": 100
				}
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &event.HttpRequestEvent{
				EventHeader: makeEventHeader(1234, "test"),
				Path:        tt.path,
				Host:        "generativelanguage.googleapis.com",
			}
			req.RequestPayload = []byte(tt.payload)

			result, err := parser.ParseRequest(req)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, event.LLMMessageTypeRequest, result.MessageType)
		})
	}
}

func TestGeminiParser_ParseResponse(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name            string
		path            string
		payload         string
		expectedModel   string
		expectedContent string
		expectedError   string
		wantErr         bool
	}{
		{
			name: "simple response",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Hello! How can I help?"}]
					},
					"finishReason": "STOP"
				}],
				"modelVersion": "gemini-2.0-flash-001"
			}`,
			expectedModel:   "gemini-2.0-flash-001",
			expectedContent: "Hello! How can I help?",
		},
		{
			name: "response with multiple text parts",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"text": "First part."},
							{"text": "Second part."}
						]
					}
				}]
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "First part.\nSecond part.",
		},
		{
			name: "error response",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"error": {
					"code": 400,
					"message": "Invalid API key",
					"status": "INVALID_ARGUMENT"
				}
			}`,
			expectedModel: "gemini-2.0-flash",
			expectedError: "Invalid API key",
		},
		{
			name: "model from path when not in response",
			path: "/v1beta/models/gemini-1.5-pro:generateContent",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Response"}]
					}
				}]
			}`,
			expectedModel:   "gemini-1.5-pro",
			expectedContent: "Response",
		},
		{
			name: "response with finish reason MAX_TOKENS",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Truncated response..."}]
					},
					"finishReason": "MAX_TOKENS"
				}],
				"modelVersion": "gemini-2.0-flash"
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "Truncated response...",
		},
		{
			name: "empty candidates",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{
				"candidates": [],
				"modelVersion": "gemini-2.0-flash"
			}`,
			expectedModel:   "gemini-2.0-flash",
			expectedContent: "",
		},
		{
			name:    "invalid JSON",
			path:    "/v1beta/models/gemini-2.0-flash:generateContent",
			payload: `{invalid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &event.HttpResponseEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "test"),
					Path:        tt.path,
					Host:        "generativelanguage.googleapis.com",
				},
			}
			resp.ResponsePayload = []byte(tt.payload)

			result, err := parser.ParseResponse(resp)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, tt.expectedError, result.Error)
			assert.Equal(t, event.LLMMessageTypeResponse, result.MessageType)
		})
	}
}

func TestGeminiParser_ParseStreamEvent(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name            string
		path            string
		data            string
		expectedModel   string
		expectedContent string
		expectedError   string
		expectedDone    bool
		wantErr         bool
	}{
		{
			name: "stream chunk with content",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Hello"}]
					}
				}]
			}`,
			expectedContent: "Hello",
			expectedDone:    false,
		},
		{
			name: "stream chunk with model version",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "World"}]
					}
				}],
				"modelVersion": "gemini-2.0-flash-001"
			}`,
			expectedModel:   "gemini-2.0-flash-001",
			expectedContent: "World",
			expectedDone:    false,
		},
		{
			name: "final stream chunk with STOP",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Done!"}]
					},
					"finishReason": "STOP"
				}],
				"usageMetadata": {
					"promptTokenCount": 10,
					"candidatesTokenCount": 20,
					"totalTokenCount": 30
				}
			}`,
			expectedContent: "Done!",
			expectedDone:    true,
		},
		{
			name: "stream ends with MAX_TOKENS",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Truncated..."}]
					},
					"finishReason": "MAX_TOKENS"
				}]
			}`,
			expectedContent: "Truncated...",
			expectedDone:    true,
		},
		{
			name: "stream ends with SAFETY",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"candidates": [{
					"finishReason": "SAFETY"
				}]
			}`,
			expectedContent: "",
			expectedDone:    true,
		},
		{
			name: "error in stream",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data: `{
				"error": {
					"code": 429,
					"message": "Rate limit exceeded",
					"status": "RESOURCE_EXHAUSTED"
				}
			}`,
			expectedError: "Rate limit exceeded",
			expectedDone:  true,
		},
		{
			name:         "empty data",
			path:         "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data:         "",
			expectedDone: false,
		},
		{
			name:         "whitespace only data",
			path:         "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data:         "   \n  ",
			expectedDone: false,
		},
		{
			name:    "invalid JSON in stream",
			path:    "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
			data:    `{invalid`,
			wantErr: true,
		},
		{
			name: "model from path when not in response",
			path: "/v1beta/models/gemini-1.5-pro:streamGenerateContent",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Chunk"}]
					}
				}]
			}`,
			expectedModel:   "gemini-1.5-pro",
			expectedContent: "Chunk",
			expectedDone:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := &event.SSEEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "test"),
					Path:        tt.path,
					Host:        "generativelanguage.googleapis.com",
				},
				Data: []byte(tt.data),
			}

			result, done, err := parser.ParseStreamEvent(sse)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedDone, done)

			if tt.data == "" || tt.data == "   \n  " {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			if tt.expectedModel != "" {
				assert.Equal(t, tt.expectedModel, result.Model)
			}
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, tt.expectedError, result.Error)
			assert.Equal(t, event.LLMMessageTypeStreamChunk, result.MessageType)
		})
	}
}

func TestGeminiParser_ExtractModelFromPath(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "standard generateContent",
			path:     "/v1beta/models/gemini-2.0-flash:generateContent",
			expected: "gemini-2.0-flash",
		},
		{
			name:     "streamGenerateContent",
			path:     "/v1beta/models/gemini-1.5-pro:streamGenerateContent",
			expected: "gemini-1.5-pro",
		},
		{
			name:     "with query params",
			path:     "/v1beta/models/gemini-2.0-flash:generateContent?key=test&alt=json",
			expected: "gemini-2.0-flash",
		},
		{
			name:     "different model name format",
			path:     "/v1beta/models/gemini-2.0-flash-exp:generateContent",
			expected: "gemini-2.0-flash-exp",
		},
		{
			name:     "no model in path",
			path:     "/v1beta/generateContent",
			expected: "",
		},
		{
			name:     "empty path",
			path:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.extractModelFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
