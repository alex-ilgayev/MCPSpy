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

func TestGeminiParser_ExtractToolUsage_FromResponse(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name          string
		payload       string
		expectedTools []struct {
			usageType event.ToolUsageType
			toolName  string
			input     string
		}
	}{
		{
			name: "single functionCall",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"functionCall": {"name": "get_weather", "args": {"location": "NYC"}}}
						]
					}
				}]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolName  string
				input     string
			}{
				{event.ToolUsageTypeInvocation, "get_weather", `{"location": "NYC"}`},
			},
		},
		{
			name: "multiple functionCalls",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"functionCall": {"name": "search", "args": {"query": "weather"}}},
							{"functionCall": {"name": "read_file", "args": {"path": "/tmp/test"}}}
						]
					}
				}]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolName  string
				input     string
			}{
				{event.ToolUsageTypeInvocation, "search", `{"query": "weather"}`},
				{event.ToolUsageTypeInvocation, "read_file", `{"path": "/tmp/test"}`},
			},
		},
		{
			name: "no functionCalls (text only)",
			payload: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Hello, world!"}]
					}
				}]
			}`,
			expectedTools: nil,
		},
		{
			name:          "invalid JSON",
			payload:       `{invalid`,
			expectedTools: nil,
		},
		{
			name: "empty candidates",
			payload: `{
				"candidates": []
			}`,
			expectedTools: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &event.HttpResponseEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "test"),
					Host:        "generativelanguage.googleapis.com",
					Path:        "/v1beta/models/gemini-2.0-flash:generateContent",
				},
				SSLContext:      12345,
				ResponsePayload: []byte(tt.payload),
			}

			result := parser.ExtractToolUsage(resp)

			if tt.expectedTools == nil {
				assert.Empty(t, result)
				return
			}

			require.Len(t, result, len(tt.expectedTools))
			for i, expected := range tt.expectedTools {
				assert.Equal(t, expected.usageType, result[i].UsageType)
				assert.Equal(t, expected.toolName, result[i].ToolName)
				assert.Equal(t, expected.input, result[i].Input)
				assert.Equal(t, uint64(12345), result[i].SessionID)
			}
		})
	}
}

func TestGeminiParser_ExtractToolUsage_FromRequest(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name          string
		payload       string
		expectedTools []struct {
			usageType event.ToolUsageType
			toolName  string
			output    string
		}
	}{
		{
			name: "single functionResponse",
			payload: `{
				"contents": [{
					"role": "user",
					"parts": [
						{"functionResponse": {"name": "get_weather", "response": {"temperature": 72, "condition": "sunny"}}}
					]
				}]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolName  string
				output    string
			}{
				{event.ToolUsageTypeResult, "get_weather", `{"temperature": 72, "condition": "sunny"}`},
			},
		},
		{
			name: "multiple functionResponses",
			payload: `{
				"contents": [{
					"role": "user",
					"parts": [
						{"functionResponse": {"name": "search", "response": {"results": ["a", "b"]}}},
						{"functionResponse": {"name": "read_file", "response": {"content": "file contents"}}}
					]
				}]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolName  string
				output    string
			}{
				{event.ToolUsageTypeResult, "search", `{"results": ["a", "b"]}`},
				{event.ToolUsageTypeResult, "read_file", `{"content": "file contents"}`},
			},
		},
		{
			name: "no functionResponse (text only)",
			payload: `{
				"contents": [{
					"role": "user",
					"parts": [{"text": "Hello"}]
				}]
			}`,
			expectedTools: nil,
		},
		{
			name:          "invalid JSON",
			payload:       `{invalid`,
			expectedTools: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &event.HttpRequestEvent{
				EventHeader:    makeEventHeader(1234, "test"),
				SSLContext:     12345,
				Host:           "generativelanguage.googleapis.com",
				Path:           "/v1beta/models/gemini-2.0-flash:generateContent",
				RequestPayload: []byte(tt.payload),
			}

			result := parser.ExtractToolUsage(req)

			if tt.expectedTools == nil {
				assert.Empty(t, result)
				return
			}

			require.Len(t, result, len(tt.expectedTools))
			for i, expected := range tt.expectedTools {
				assert.Equal(t, expected.usageType, result[i].UsageType)
				assert.Equal(t, expected.toolName, result[i].ToolName)
				assert.Equal(t, expected.output, result[i].Output)
				assert.Equal(t, uint64(12345), result[i].SessionID)
			}
		})
	}
}

func TestGeminiParser_ExtractToolUsage_SSE(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name          string
		data          string
		expectedTools []struct {
			usageType event.ToolUsageType
			toolName  string
			input     string
		}
	}{
		{
			name: "streaming chunk with functionCall",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"functionCall": {"name": "search", "args": {"query": "weather"}}}
						]
					}
				}]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolName  string
				input     string
			}{
				{event.ToolUsageTypeInvocation, "search", `{"query": "weather"}`},
			},
		},
		{
			name: "streaming chunk with text (no tools)",
			data: `{
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [{"text": "Hello"}]
					}
				}]
			}`,
			expectedTools: nil,
		},
		{
			name:          "empty data",
			data:          "",
			expectedTools: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := &event.SSEEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "test"),
					Host:        "generativelanguage.googleapis.com",
					Path:        "/v1beta/models/gemini-2.0-flash:streamGenerateContent",
				},
				SSLContext: 12345,
				Data:       []byte(tt.data),
			}

			result := parser.ExtractToolUsage(sse)

			if tt.expectedTools == nil {
				assert.Empty(t, result)
				return
			}

			require.Len(t, result, len(tt.expectedTools))
			for i, expected := range tt.expectedTools {
				assert.Equal(t, expected.usageType, result[i].UsageType)
				assert.Equal(t, expected.toolName, result[i].ToolName)
				assert.Equal(t, expected.input, result[i].Input)
				assert.Equal(t, uint64(12345), result[i].SessionID)
			}
		})
	}
}

// Cloudcode (Gemini CLI) format tests
// Cloudcode uses a wrapper format: {"model": "...", "request": {...}} for requests
// and {"response": {...}, "traceId": "..."} for responses

func TestGeminiParser_ParseRequest_Cloudcode(t *testing.T) {
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
			name: "cloudcode wrapped request",
			path: "/v1internal:generateContent",
			payload: `{
				"model": "gemini-3-flash-preview",
				"project": "test-project",
				"request": {
					"contents": [{"role": "user", "parts": [{"text": "Hello from Gemini CLI!"}]}]
				}
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "Hello from Gemini CLI!",
		},
		{
			name: "cloudcode with system instruction",
			path: "/v1internal:streamGenerateContent?alt=sse",
			payload: `{
				"model": "gemini-2.5-flash-lite",
				"project": "fast-disk-cvpmb",
				"request": {
					"contents": [{"role": "user", "parts": [{"text": "Execute this task"}]}],
					"systemInstruction": {
						"parts": [{"text": "You are a helpful assistant"}],
						"role": "user"
					}
				}
			}`,
			expectedModel:   "gemini-2.5-flash-lite",
			expectedContent: "Execute this task",
		},
		{
			name: "cloudcode with conversation history",
			path: "/v1internal:streamGenerateContent?alt=sse",
			payload: `{
				"model": "gemini-3-flash-preview",
				"request": {
					"contents": [
						{"role": "user", "parts": [{"text": "First message"}]},
						{"role": "model", "parts": [{"text": "First response"}]},
						{"role": "user", "parts": [{"text": "Follow-up question"}]}
					]
				}
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "Follow-up question",
		},
		{
			name: "cloudcode with function responses",
			path: "/v1internal:streamGenerateContent?alt=sse",
			payload: `{
				"model": "gemini-3-flash-preview",
				"request": {
					"contents": [
						{"role": "user", "parts": [{"text": "Read the file"}]},
						{"role": "model", "parts": [{"functionCall": {"name": "read_file", "args": {"path": "test.txt"}}}]},
						{"role": "user", "parts": [{"functionResponse": {"name": "read_file", "response": {"output": "File content"}}}]},
						{"role": "user", "parts": [{"text": "What did the file contain?"}]}
					]
				}
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "What did the file contain?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &event.HttpRequestEvent{
				EventHeader: makeEventHeader(1234, "node"),
				Path:        tt.path,
				Host:        "cloudcode-pa.googleapis.com",
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

func TestGeminiParser_ParseResponse_Cloudcode(t *testing.T) {
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
			name: "cloudcode wrapped response",
			path: "/v1internal:generateContent",
			payload: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{"text": "Hello from Gemini!"}]
						},
						"finishReason": "STOP"
					}],
					"modelVersion": "gemini-2.5-flash-lite"
				},
				"traceId": "abc123"
			}`,
			expectedModel:   "gemini-2.5-flash-lite",
			expectedContent: "Hello from Gemini!",
		},
		{
			name: "cloudcode response with function call",
			path: "/v1internal:generateContent",
			payload: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [
								{"text": "I will read the file."},
								{"functionCall": {"name": "read_file", "args": {"file_path": "test.txt"}}}
							]
						},
						"finishReason": "STOP"
					}],
					"modelVersion": "gemini-3-flash-preview"
				},
				"traceId": "xyz789"
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "I will read the file.",
		},
		{
			name: "cloudcode error response",
			path: "/v1internal:generateContent",
			payload: `{
				"response": {
					"error": {
						"code": 429,
						"message": "Rate limit exceeded",
						"status": "RESOURCE_EXHAUSTED"
					}
				},
				"traceId": "err123"
			}`,
			expectedModel: "",
			expectedError: "Rate limit exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &event.HttpResponseEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "node"),
					Path:        tt.path,
					Host:        "cloudcode-pa.googleapis.com",
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

func TestGeminiParser_ParseStreamEvent_Cloudcode(t *testing.T) {
	parser := NewGeminiParser()

	tests := []struct {
		name            string
		path            string
		data            string
		expectedModel   string
		expectedContent string
		expectedDone    bool
		expectedError   string
	}{
		{
			name: "cloudcode SSE with thinking",
			path: "/v1internal:streamGenerateContent?alt=sse",
			data: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{"thought": true, "text": "Analyzing the request..."}]
						}
					}],
					"modelVersion": "gemini-3-flash-preview"
				},
				"traceId": "think123"
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "Analyzing the request...",
			expectedDone:    false,
		},
		{
			name: "cloudcode SSE with content",
			path: "/v1internal:streamGenerateContent?alt=sse",
			data: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{"text": "Here is the response."}]
						}
					}],
					"modelVersion": "gemini-3-flash-preview"
				},
				"traceId": "content123"
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "Here is the response.",
			expectedDone:    false,
		},
		{
			name: "cloudcode SSE final chunk with STOP",
			path: "/v1internal:streamGenerateContent?alt=sse",
			data: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{"text": "Final output."}]
						},
						"finishReason": "STOP"
					}],
					"usageMetadata": {
						"promptTokenCount": 100,
						"candidatesTokenCount": 50,
						"totalTokenCount": 150
					},
					"modelVersion": "gemini-3-flash-preview"
				},
				"traceId": "final123"
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "Final output.",
			expectedDone:    true,
		},
		{
			name: "cloudcode SSE with function call",
			path: "/v1internal:streamGenerateContent?alt=sse",
			data: `{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{
								"functionCall": {
									"name": "run_shell_command",
									"args": {"command": "echo hello"}
								}
							}]
						},
						"finishReason": "STOP"
					}],
					"modelVersion": "gemini-3-flash-preview"
				},
				"traceId": "fc123"
			}`,
			expectedModel:   "gemini-3-flash-preview",
			expectedContent: "",
			expectedDone:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := &event.SSEEvent{
				HttpRequestEvent: event.HttpRequestEvent{
					EventHeader: makeEventHeader(1234, "node"),
					Host:        "cloudcode-pa.googleapis.com",
					Path:        tt.path,
				},
				SSLContext: 12345,
				Data:       []byte(tt.data),
			}

			result, done, err := parser.ParseStreamEvent(sse)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedModel, result.Model)
			assert.Equal(t, tt.expectedContent, result.Content)
			assert.Equal(t, tt.expectedDone, done)
			assert.Equal(t, tt.expectedError, result.Error)
			assert.Equal(t, event.LLMMessageTypeStreamChunk, result.MessageType)
		})
	}
}

func TestGeminiParser_ExtractToolUsage_Cloudcode(t *testing.T) {
	parser := NewGeminiParser()

	t.Run("cloudcode response with function calls", func(t *testing.T) {
		resp := &event.HttpResponseEvent{
			HttpRequestEvent: event.HttpRequestEvent{
				EventHeader: makeEventHeader(1234, "node"),
				Path:        "/v1internal:generateContent",
				Host:        "cloudcode-pa.googleapis.com",
			},
		}
		resp.ResponsePayload = []byte(`{
			"response": {
				"candidates": [{
					"content": {
						"role": "model",
						"parts": [
							{"functionCall": {"name": "read_file", "args": {"file_path": "test.txt"}}},
							{"functionCall": {"name": "run_shell_command", "args": {"command": "ls -la"}}}
						]
					}
				}]
			},
			"traceId": "tools123"
		}`)

		result := parser.ExtractToolUsage(resp)

		require.Len(t, result, 2)
		assert.Equal(t, event.ToolUsageTypeInvocation, result[0].UsageType)
		assert.Equal(t, "read_file", result[0].ToolName)
		assert.Equal(t, `{"file_path": "test.txt"}`, result[0].Input)
		assert.Equal(t, event.ToolUsageTypeInvocation, result[1].UsageType)
		assert.Equal(t, "run_shell_command", result[1].ToolName)
		assert.Equal(t, `{"command": "ls -la"}`, result[1].Input)
	})

	t.Run("cloudcode request with function responses", func(t *testing.T) {
		req := &event.HttpRequestEvent{
			EventHeader: makeEventHeader(1234, "node"),
			Path:        "/v1internal:streamGenerateContent?alt=sse",
			Host:        "cloudcode-pa.googleapis.com",
		}
		req.RequestPayload = []byte(`{
			"model": "gemini-3-flash-preview",
			"request": {
				"contents": [
					{"role": "user", "parts": [{"text": "Read the file"}]},
					{"role": "model", "parts": [{"functionCall": {"name": "read_file", "args": {"file_path": "test.txt"}}}]},
					{"parts": [{"functionResponse": {"name": "read_file", "response": {"output": "File content here"}}}]}
				]
			}
		}`)

		result := parser.ExtractToolUsage(req)

		require.Len(t, result, 1)
		assert.Equal(t, event.ToolUsageTypeResult, result[0].UsageType)
		assert.Equal(t, "read_file", result[0].ToolName)
		assert.Equal(t, `{"output": "File content here"}`, result[0].Output)
	})

	t.Run("cloudcode SSE with function call", func(t *testing.T) {
		sse := &event.SSEEvent{
			HttpRequestEvent: event.HttpRequestEvent{
				EventHeader: makeEventHeader(1234, "node"),
				Host:        "cloudcode-pa.googleapis.com",
				Path:        "/v1internal:streamGenerateContent?alt=sse",
			},
			SSLContext: 12345,
			Data: []byte(`{
				"response": {
					"candidates": [{
						"content": {
							"role": "model",
							"parts": [{
								"functionCall": {
									"name": "search_file_content",
									"args": {"pattern": "TODO", "dir_path": "."}
								}
							}]
						}
					}]
				},
				"traceId": "sse-tool123"
			}`),
		}

		result := parser.ExtractToolUsage(sse)

		require.Len(t, result, 1)
		assert.Equal(t, event.ToolUsageTypeInvocation, result[0].UsageType)
		assert.Equal(t, "search_file_content", result[0].ToolName)
		assert.Equal(t, `{"pattern": "TODO", "dir_path": "."}`, result[0].Input)
		assert.Equal(t, uint64(12345), result[0].SessionID)
	})
}
