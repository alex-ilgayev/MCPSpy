package providers

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

func TestOpenAIParser_ParseRequest(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name        string
		body        string
		wantModel   string
		wantStream  bool
		wantMsgLen  int
		wantErr     bool
	}{
		{
			name: "basic chat request",
			body: `{
				"model": "gpt-4",
				"messages": [
					{"role": "system", "content": "You are helpful."},
					{"role": "user", "content": "Hello"}
				],
				"stream": false
			}`,
			wantModel:  "gpt-4",
			wantStream: false,
			wantMsgLen: 2,
		},
		{
			name: "streaming request",
			body: `{
				"model": "gpt-4-turbo",
				"messages": [{"role": "user", "content": "Hi"}],
				"stream": true
			}`,
			wantModel:  "gpt-4-turbo",
			wantStream: true,
			wantMsgLen: 1,
		},
		{
			name:    "invalid json",
			body:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := event.LLMTransport{PID: 123}
			got, err := parser.ParseRequest([]byte(tt.body), transport, "/v1/chat/completions")

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if got.Model != tt.wantModel {
				t.Errorf("ParseRequest() model = %v, want %v", got.Model, tt.wantModel)
			}
			if got.IsStreaming != tt.wantStream {
				t.Errorf("ParseRequest() streaming = %v, want %v", got.IsStreaming, tt.wantStream)
			}
			if len(got.Messages) != tt.wantMsgLen {
				t.Errorf("ParseRequest() messages len = %v, want %v", len(got.Messages), tt.wantMsgLen)
			}
			if got.MessageType != event.LLMMessageTypeRequest {
				t.Errorf("ParseRequest() type = %v, want %v", got.MessageType, event.LLMMessageTypeRequest)
			}
			if got.Provider != event.ProviderOpenAI {
				t.Errorf("ParseRequest() provider = %v, want %v", got.Provider, event.ProviderOpenAI)
			}
		})
	}
}

func TestOpenAIParser_ParseResponse(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name            string
		body            string
		statusCode      int
		wantModel       string
		wantContent     string
		wantToolCalls   int
		wantFinish      string
		wantInputTokens int
		wantErr         bool
		wantErrMsg      string
	}{
		{
			name: "successful response",
			body: `{
				"id": "chatcmpl-123",
				"object": "chat.completion",
				"model": "gpt-4",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Hello! How can I help?"
					},
					"finish_reason": "stop"
				}],
				"usage": {
					"prompt_tokens": 10,
					"completion_tokens": 5,
					"total_tokens": 15
				}
			}`,
			statusCode:      200,
			wantModel:       "gpt-4",
			wantContent:     "Hello! How can I help?",
			wantFinish:      "stop",
			wantInputTokens: 10,
		},
		{
			name: "response with tool calls",
			body: `{
				"id": "chatcmpl-456",
				"model": "gpt-4",
				"choices": [{
					"message": {
						"role": "assistant",
						"content": null,
						"tool_calls": [{
							"id": "call_123",
							"type": "function",
							"function": {
								"name": "get_weather",
								"arguments": "{\"location\": \"SF\"}"
							}
						}]
					},
					"finish_reason": "tool_calls"
				}]
			}`,
			statusCode:    200,
			wantModel:     "gpt-4",
			wantToolCalls: 1,
			wantFinish:    "tool_calls",
		},
		{
			name: "error response",
			body: `{
				"error": {
					"message": "Rate limit exceeded",
					"type": "rate_limit_error",
					"code": "rate_limit"
				}
			}`,
			statusCode: 429,
			wantErrMsg: "Rate limit exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := event.LLMTransport{PID: 123}
			got, err := parser.ParseResponse([]byte(tt.body), transport, "/v1/chat/completions", tt.statusCode)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if tt.wantErrMsg != "" {
				if got.Error == nil || got.Error.Message != tt.wantErrMsg {
					t.Errorf("ParseResponse() error message = %v, want %v", got.Error, tt.wantErrMsg)
				}
				return
			}

			if got.Model != tt.wantModel {
				t.Errorf("ParseResponse() model = %v, want %v", got.Model, tt.wantModel)
			}
			if got.FinishReason != tt.wantFinish {
				t.Errorf("ParseResponse() finish = %v, want %v", got.FinishReason, tt.wantFinish)
			}
			if len(got.ToolCalls) != tt.wantToolCalls {
				t.Errorf("ParseResponse() tool calls = %v, want %v", len(got.ToolCalls), tt.wantToolCalls)
			}
			if tt.wantContent != "" && (len(got.Messages) == 0 || got.Messages[0].Content != tt.wantContent) {
				t.Errorf("ParseResponse() content mismatch")
			}
			if tt.wantInputTokens > 0 && (got.Usage == nil || got.Usage.InputTokens != tt.wantInputTokens) {
				t.Errorf("ParseResponse() usage mismatch")
			}
		})
	}
}

func TestOpenAIParser_ParseStreamChunk(t *testing.T) {
	parser := NewOpenAIParser()

	tests := []struct {
		name        string
		data        string
		wantType    event.LLMMessageType
		wantContent string
		wantModel   string
		wantErr     bool
	}{
		{
			name: "content chunk",
			data: `{"id":"chatcmpl-123","model":"gpt-4","choices":[{"delta":{"content":"Hello"}}]}`,
			wantType:    event.LLMMessageTypeStreamChunk,
			wantContent: "Hello",
			wantModel:   "gpt-4",
		},
		{
			name:     "done marker",
			data:     "[DONE]",
			wantType: event.LLMMessageTypeStreamEnd,
		},
		{
			name:     "done marker with whitespace",
			data:     "  [DONE]  ",
			wantType: event.LLMMessageTypeStreamEnd,
		},
		{
			name: "finish reason chunk",
			data: `{"id":"chatcmpl-123","model":"gpt-4","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			wantType: event.LLMMessageTypeStreamChunk,
		},
		{
			name:    "invalid json",
			data:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := event.LLMTransport{PID: 123}
			got, err := parser.ParseStreamChunk(tt.data, transport, "/v1/chat/completions", 0)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStreamChunk() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if got.MessageType != tt.wantType {
				t.Errorf("ParseStreamChunk() type = %v, want %v", got.MessageType, tt.wantType)
			}
			if tt.wantContent != "" && got.ChunkContent != tt.wantContent {
				t.Errorf("ParseStreamChunk() content = %v, want %v", got.ChunkContent, tt.wantContent)
			}
			if tt.wantModel != "" && got.Model != tt.wantModel {
				t.Errorf("ParseStreamChunk() model = %v, want %v", got.Model, tt.wantModel)
			}
		})
	}
}

func TestExtractMessageContent(t *testing.T) {
	tests := []struct {
		name    string
		content interface{}
		want    string
	}{
		{
			name:    "string content",
			content: "Hello world",
			want:    "Hello world",
		},
		{
			name:    "nil content",
			content: nil,
			want:    "",
		},
		{
			name: "multimodal content",
			content: []interface{}{
				map[string]interface{}{"type": "text", "text": "First part"},
				map[string]interface{}{"type": "image", "image_url": "..."},
				map[string]interface{}{"type": "text", "text": "Second part"},
			},
			want: "First part\nSecond part",
		},
		{
			name:    "unknown type",
			content: 12345,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMessageContent(tt.content)
			if got != tt.want {
				t.Errorf("extractMessageContent() = %v, want %v", got, tt.want)
			}
		})
	}
}
