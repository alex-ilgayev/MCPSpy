package providers

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create EventHeader with comm string
func makeEventHeader(pid uint32, comm string) event.EventHeader {
	header := event.EventHeader{
		PID: pid,
	}
	copy(header.CommBytes[:], comm)
	return header
}

func TestAnthropicParser_ParseRequest(t *testing.T) {
	parser := NewAnthropicParser()

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
				"model": "claude-sonnet-4-20250514",
				"messages": [{"role": "user", "content": "Hello, world!"}]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "Hello, world!",
		},
		{
			name: "array content blocks",
			payload: `{
				"model": "claude-haiku-4-5-20251001",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "text", "text": "First paragraph."},
						{"type": "text", "text": "Second paragraph."}
					]
				}]
			}`,
			expectedModel:   "claude-haiku-4-5-20251001",
			expectedContent: "First paragraph.\nSecond paragraph.",
		},
		{
			name: "multiple messages extracts last user message",
			payload: `{
				"model": "claude-opus-4-20250514",
				"messages": [
					{"role": "user", "content": "First question"},
					{"role": "assistant", "content": "First answer"},
					{"role": "user", "content": "Follow-up question"}
				]
			}`,
			expectedModel:   "claude-opus-4-20250514",
			expectedContent: "Follow-up question",
		},
		{
			name: "system and user messages",
			payload: `{
				"model": "claude-sonnet-4-20250514",
				"messages": [
					{"role": "system", "content": "You are helpful"},
					{"role": "user", "content": "What is 2+2?"}
				]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "What is 2+2?",
		},
		{
			name: "mixed content types in array",
			payload: `{
				"model": "claude-sonnet-4-20250514",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "image", "source": {"type": "base64", "data": "..."}},
						{"type": "text", "text": "What is in this image?"}
					]
				}]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "What is in this image?",
		},
		{
			name:    "invalid JSON",
			payload: `{invalid json`,
			wantErr: true,
		},
		{
			name: "empty messages array",
			payload: `{
				"model": "claude-sonnet-4-20250514",
				"messages": []
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &event.HttpRequestEvent{
				EventHeader:    makeEventHeader(1234, "python"),
				SSLContext:     99999,
				Host:           "api.anthropic.com",
				Path:           "/v1/messages",
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
			assert.Equal(t, "api.anthropic.com", result.Host)
			assert.Equal(t, "/v1/messages", result.Path)
			assert.Equal(t, uint64(99999), result.SessionID)
		})
	}
}

func TestAnthropicParser_ParseResponse(t *testing.T) {
	parser := NewAnthropicParser()

	tests := []struct {
		name            string
		payload         string
		expectedModel   string
		expectedContent string
		expectedError   string
		wantErr         bool
	}{
		{
			name: "successful response with single text block",
			payload: `{
				"type": "message",
				"model": "claude-sonnet-4-20250514",
				"content": [{"type": "text", "text": "Hello! How can I help you?"}]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "Hello! How can I help you?",
		},
		{
			name: "successful response with multiple text blocks",
			payload: `{
				"type": "message",
				"model": "claude-sonnet-4-20250514",
				"content": [
					{"type": "text", "text": "First part."},
					{"type": "text", "text": "Second part."}
				]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "First part.\nSecond part.",
		},
		{
			name: "error response",
			payload: `{
				"type": "error",
				"error": {"type": "invalid_request_error", "message": "Invalid API key"}
			}`,
			expectedError: "Invalid API key",
		},
		{
			name: "overloaded error",
			payload: `{
				"type": "error",
				"error": {"type": "overloaded_error", "message": "Overloaded"}
			}`,
			expectedError: "Overloaded",
		},
		{
			name: "response with tool use block",
			payload: `{
				"type": "message",
				"model": "claude-sonnet-4-20250514",
				"content": [
					{"type": "text", "text": "Let me search for that."},
					{"type": "tool_use", "id": "toolu_123", "name": "search", "input": {"query": "weather"}}
				]
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "Let me search for that.",
		},
		{
			name:    "invalid JSON",
			payload: `not json`,
			wantErr: true,
		},
		{
			name: "empty content array",
			payload: `{
				"type": "message",
				"model": "claude-sonnet-4-20250514",
				"content": []
			}`,
			expectedModel:   "claude-sonnet-4-20250514",
			expectedContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &event.HttpResponseEvent{
				EventHeader: makeEventHeader(5678, "curl"),
				HttpRequestEvent: event.HttpRequestEvent{
					Host: "api.anthropic.com",
					Path: "/v1/messages",
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

func TestAnthropicParser_ParseStreamEvent(t *testing.T) {
	parser := NewAnthropicParser()

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
			name:          "message_start extracts model",
			data:          `{"type":"message_start","message":{"model":"claude-haiku-4-5-20251001","id":"msg_123","type":"message","role":"assistant","content":[]}}`,
			expectedModel: "claude-haiku-4-5-20251001",
			expectedDone:  false,
		},
		{
			name:         "content_block_start",
			data:         `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			expectedDone: false,
		},
		{
			name:            "content_block_delta with text",
			data:            `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			expectedContent: "Hello",
			expectedDone:    false,
		},
		{
			name:            "content_block_delta with longer text",
			data:            `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world! How are you today?"}}`,
			expectedContent: " world! How are you today?",
			expectedDone:    false,
		},
		{
			name:         "content_block_stop",
			data:         `{"type":"content_block_stop","index":0}`,
			expectedDone: false,
		},
		{
			name:         "message_delta",
			data:         `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":10}}`,
			expectedDone: false,
		},
		{
			name:         "message_stop signals done",
			data:         `{"type":"message_stop"}`,
			expectedDone: true,
		},
		{
			name:         "ping event",
			data:         `{"type":"ping"}`,
			expectedDone: false,
		},
		{
			name:          "error event",
			data:          `{"type":"error","error":{"type":"overloaded_error","message":"Server overloaded"}}`,
			expectedError: "Server overloaded",
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
					Host: "api.anthropic.com",
					Path: "/v1/messages",
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

			// For empty/whitespace data, result may be nil
			if tt.data == "" || tt.data == "   \n\t  " {
				assert.Nil(t, result)
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

func TestExtractUserPrompt(t *testing.T) {
	tests := []struct {
		name     string
		messages []anthropicMessage
		expected string
	}{
		{
			name:     "empty messages",
			messages: []anthropicMessage{},
			expected: "",
		},
		{
			name: "single user message",
			messages: []anthropicMessage{
				{Role: "user", Content: "Hello"},
			},
			expected: "Hello",
		},
		{
			name: "user and assistant messages",
			messages: []anthropicMessage{
				{Role: "user", Content: "Question 1"},
				{Role: "assistant", Content: "Answer 1"},
				{Role: "user", Content: "Question 2"},
			},
			expected: "Question 2",
		},
		{
			name: "only assistant message",
			messages: []anthropicMessage{
				{Role: "assistant", Content: "I can help"},
			},
			expected: "",
		},
		{
			name: "nil content",
			messages: []anthropicMessage{
				{Role: "user", Content: nil},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractUserPrompt(tt.messages)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractMessageContent(t *testing.T) {
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
			name: "mixed block types",
			content: []interface{}{
				map[string]interface{}{"type": "image", "source": "data"},
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
			result := extractMessageContent(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractResponseText(t *testing.T) {
	tests := []struct {
		name     string
		blocks   []anthropicContentBlock
		expected string
	}{
		{
			name:     "empty blocks",
			blocks:   []anthropicContentBlock{},
			expected: "",
		},
		{
			name: "single text block",
			blocks: []anthropicContentBlock{
				{Type: "text", Text: "Hello"},
			},
			expected: "Hello",
		},
		{
			name: "multiple text blocks",
			blocks: []anthropicContentBlock{
				{Type: "text", Text: "First"},
				{Type: "text", Text: "Second"},
			},
			expected: "First\nSecond",
		},
		{
			name: "text and tool_use blocks",
			blocks: []anthropicContentBlock{
				{Type: "text", Text: "Let me help"},
				{Type: "tool_use", Text: ""},
			},
			expected: "Let me help",
		},
		{
			name: "empty text in block",
			blocks: []anthropicContentBlock{
				{Type: "text", Text: ""},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractResponseText(tt.blocks)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAnthropicParser_ExtractToolUsage_FromResponse(t *testing.T) {
	parser := NewAnthropicParser()

	tests := []struct {
		name          string
		payload       string
		expectedTools []struct {
			usageType event.ToolUsageType
			toolID    string
			toolName  string
			input     string
		}
	}{
		{
			name: "single tool_use block",
			payload: `{
				"content": [
					{"type": "text", "text": "Let me search for that."},
					{"type": "tool_use", "id": "toolu_123", "name": "search", "input": {"query": "weather"}}
				]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolID    string
				toolName  string
				input     string
			}{
				{event.ToolUsageTypeInvocation, "toolu_123", "search", `{"query": "weather"}`},
			},
		},
		{
			name: "multiple tool_use blocks",
			payload: `{
				"content": [
					{"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": {"path": "/tmp/test.txt"}},
					{"type": "tool_use", "id": "toolu_2", "name": "write_file", "input": {"path": "/tmp/out.txt", "content": "hello"}}
				]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolID    string
				toolName  string
				input     string
			}{
				{event.ToolUsageTypeInvocation, "toolu_1", "read_file", `{"path": "/tmp/test.txt"}`},
				{event.ToolUsageTypeInvocation, "toolu_2", "write_file", `{"path": "/tmp/out.txt", "content": "hello"}`},
			},
		},
		{
			name: "no tool_use blocks",
			payload: `{
				"content": [
					{"type": "text", "text": "Hello, world!"}
				]
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
			resp := &event.HttpResponseEvent{
				EventHeader: makeEventHeader(1234, "python"),
				HttpRequestEvent: event.HttpRequestEvent{
					Host: "api.anthropic.com",
					Path: "/v1/messages",
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
				assert.Equal(t, expected.toolID, result[i].ToolID)
				assert.Equal(t, expected.toolName, result[i].ToolName)
				assert.Equal(t, expected.input, result[i].Input)
				assert.Equal(t, uint64(12345), result[i].SessionID)
			}
		})
	}
}

func TestAnthropicParser_ExtractToolUsage_FromRequest(t *testing.T) {
	tests := []struct {
		name          string
		payload       string
		expectedTools []struct {
			usageType event.ToolUsageType
			toolID    string
			output    string
			isError   bool
		}
	}{
		{
			name: "single tool_result block",
			payload: `{
				"messages": [
					{
						"role": "user",
						"content": [
							{"type": "tool_result", "tool_use_id": "toolu_123", "content": "Search results: sunny, 72F"}
						]
					}
				]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolID    string
				output    string
				isError   bool
			}{
				{event.ToolUsageTypeResult, "toolu_123", `"Search results: sunny, 72F"`, false},
			},
		},
		{
			name: "tool_result with error",
			payload: `{
				"messages": [
					{
						"role": "user",
						"content": [
							{"type": "tool_result", "tool_use_id": "toolu_456", "content": "File not found", "is_error": true}
						]
					}
				]
			}`,
			expectedTools: []struct {
				usageType event.ToolUsageType
				toolID    string
				output    string
				isError   bool
			}{
				{event.ToolUsageTypeResult, "toolu_456", `"File not found"`, true},
			},
		},
		{
			name: "no tool_result blocks",
			payload: `{
				"messages": [
					{
						"role": "user",
						"content": [
							{"type": "text", "text": "Hello"}
						]
					}
				]
			}`,
			expectedTools: nil,
		},
		{
			name: "tool_result in assistant message (should be ignored)",
			payload: `{
				"messages": [
					{
						"role": "assistant",
						"content": [
							{"type": "tool_result", "tool_use_id": "toolu_789", "content": "result"}
						]
					}
				]
			}`,
			expectedTools: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a fresh parser for each test to avoid dedup interference
			parser := NewAnthropicParser()

			req := &event.HttpRequestEvent{
				EventHeader:    makeEventHeader(1234, "python"),
				SSLContext:     12345,
				Host:           "api.anthropic.com",
				Path:           "/v1/messages",
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
				assert.Equal(t, expected.toolID, result[i].ToolID)
				assert.Equal(t, expected.output, result[i].Output)
				assert.Equal(t, expected.isError, result[i].IsError)
				assert.Equal(t, uint64(12345), result[i].SessionID)
			}
		})
	}
}

func TestAnthropicParser_ExtractToolUsage_Deduplication(t *testing.T) {
	parser := NewAnthropicParser()

	payload := `{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "tool_result", "tool_use_id": "toolu_dedup", "content": "result1"}
				]
			}
		]
	}`

	req := &event.HttpRequestEvent{
		EventHeader:    makeEventHeader(1234, "python"),
		SSLContext:     12345,
		Host:           "api.anthropic.com",
		Path:           "/v1/messages",
		RequestPayload: []byte(payload),
	}

	// First extraction should return the tool result
	result1 := parser.ExtractToolUsage(req)
	require.Len(t, result1, 1)
	assert.Equal(t, "toolu_dedup", result1[0].ToolID)

	// Second extraction of same result should be deduplicated
	result2 := parser.ExtractToolUsage(req)
	assert.Empty(t, result2, "second extraction should be deduplicated")
}

func TestAnthropicParser_ExtractToolUsage_SessionIsolation(t *testing.T) {
	parser := NewAnthropicParser()

	payload := `{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "tool_result", "tool_use_id": "toolu_session", "content": "result"}
				]
			}
		]
	}`

	// Request from session 1
	req1 := &event.HttpRequestEvent{
		EventHeader:    makeEventHeader(1234, "python"),
		SSLContext:     11111,
		Host:           "api.anthropic.com",
		Path:           "/v1/messages",
		RequestPayload: []byte(payload),
	}

	// Request from session 2 (same tool_use_id)
	req2 := &event.HttpRequestEvent{
		EventHeader:    makeEventHeader(1234, "python"),
		SSLContext:     22222,
		Host:           "api.anthropic.com",
		Path:           "/v1/messages",
		RequestPayload: []byte(payload),
	}

	// Both should return the tool result (different sessions)
	result1 := parser.ExtractToolUsage(req1)
	require.Len(t, result1, 1)
	assert.Equal(t, uint64(11111), result1[0].SessionID)

	result2 := parser.ExtractToolUsage(req2)
	require.Len(t, result2, 1, "same tool_use_id in different session should not be deduplicated")
	assert.Equal(t, uint64(22222), result2[0].SessionID)
}

func TestAnthropicParser_ExtractToolUsage_ToolNameCorrelation(t *testing.T) {
	parser := NewAnthropicParser()

	// First, simulate a response with tool_use
	responsePayload := `{
		"content": [
			{"type": "tool_use", "id": "toolu_corr", "name": "read_file", "input": {"path": "/test"}}
		]
	}`
	resp := &event.HttpResponseEvent{
		EventHeader: makeEventHeader(1234, "python"),
		HttpRequestEvent: event.HttpRequestEvent{
			Host: "api.anthropic.com",
			Path: "/v1/messages",
		},
		SSLContext:      12345,
		ResponsePayload: []byte(responsePayload),
	}

	toolCalls := parser.ExtractToolUsage(resp)
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "read_file", toolCalls[0].ToolName)

	// Then, simulate the following request with tool_result
	requestPayload := `{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "tool_result", "tool_use_id": "toolu_corr", "content": "file contents"}
				]
			}
		]
	}`
	req := &event.HttpRequestEvent{
		EventHeader:    makeEventHeader(1234, "python"),
		SSLContext:     12345,
		Host:           "api.anthropic.com",
		Path:           "/v1/messages",
		RequestPayload: []byte(requestPayload),
	}

	toolResults := parser.ExtractToolUsage(req)
	require.Len(t, toolResults, 1)
	assert.Equal(t, "read_file", toolResults[0].ToolName, "tool name should be correlated from previous tool_use")
}

func TestAnthropicParser_ExtractToolUsage_SSE(t *testing.T) {
	parser := NewAnthropicParser()
	sessionID := uint64(99999)

	// Simulate streaming tool_use sequence
	contentBlockStart := `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_stream","name":"search"}}`
	contentBlockDelta1 := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`
	contentBlockDelta2 := `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"weather\"}"}}`
	contentBlockStop := `{"type":"content_block_stop","index":0}`

	// content_block_start - should return nil (accumulating)
	sse1 := &event.SSEEvent{
		EventHeader: makeEventHeader(1234, "node"),
		HttpRequestEvent: event.HttpRequestEvent{
			Host: "api.anthropic.com",
			Path: "/v1/messages",
		},
		SSLContext: sessionID,
		Data:       []byte(contentBlockStart),
	}
	result1 := parser.ExtractToolUsage(sse1)
	assert.Empty(t, result1)

	// content_block_delta - should return nil (accumulating)
	sse2 := &event.SSEEvent{
		EventHeader: makeEventHeader(1234, "node"),
		HttpRequestEvent: event.HttpRequestEvent{
			Host: "api.anthropic.com",
			Path: "/v1/messages",
		},
		SSLContext: sessionID,
		Data:       []byte(contentBlockDelta1),
	}
	result2 := parser.ExtractToolUsage(sse2)
	assert.Empty(t, result2)

	// second delta
	sse3 := &event.SSEEvent{
		EventHeader: makeEventHeader(1234, "node"),
		HttpRequestEvent: event.HttpRequestEvent{
			Host: "api.anthropic.com",
			Path: "/v1/messages",
		},
		SSLContext: sessionID,
		Data:       []byte(contentBlockDelta2),
	}
	result3 := parser.ExtractToolUsage(sse3)
	assert.Empty(t, result3)

	// content_block_stop - should return completed tool event
	sse4 := &event.SSEEvent{
		EventHeader: makeEventHeader(1234, "node"),
		HttpRequestEvent: event.HttpRequestEvent{
			Host: "api.anthropic.com",
			Path: "/v1/messages",
		},
		SSLContext: sessionID,
		Data:       []byte(contentBlockStop),
	}
	result4 := parser.ExtractToolUsage(sse4)
	require.Len(t, result4, 1)
	assert.Equal(t, event.ToolUsageTypeInvocation, result4[0].UsageType)
	assert.Equal(t, "toolu_stream", result4[0].ToolID)
	assert.Equal(t, "search", result4[0].ToolName)
	assert.Equal(t, `{"query":"weather"}`, result4[0].Input)
	assert.Equal(t, sessionID, result4[0].SessionID)
}
