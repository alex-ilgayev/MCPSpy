package llm

import "testing"

func TestIsAnthropicRequest(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
		want bool
	}{
		{
			name: "valid anthropic messages endpoint",
			host: "api.anthropic.com",
			path: "/v1/messages",
			want: true,
		},
		{
			name: "anthropic with query params",
			host: "api.anthropic.com",
			path: "/v1/messages?version=2023-06-01",
			want: true,
		},
		{
			name: "anthropic case insensitive host",
			host: "API.ANTHROPIC.COM",
			path: "/v1/messages",
			want: true,
		},
		{
			name: "wrong host",
			host: "api.openai.com",
			path: "/v1/messages",
			want: false,
		},
		{
			name: "wrong path",
			host: "api.anthropic.com",
			path: "/v1/complete",
			want: false,
		},
		{
			name: "empty host",
			host: "",
			path: "/v1/messages",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAnthropicRequest(tt.host, tt.path)
			if got != tt.want {
				t.Errorf("IsAnthropicRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStreamingRequest(t *testing.T) {
	tests := []struct {
		name string
		body map[string]interface{}
		want bool
	}{
		{
			name: "streaming true",
			body: map[string]interface{}{"stream": true},
			want: true,
		},
		{
			name: "streaming false",
			body: map[string]interface{}{"stream": false},
			want: false,
		},
		{
			name: "no stream field",
			body: map[string]interface{}{"model": "claude-3"},
			want: false,
		},
		{
			name: "empty body",
			body: map[string]interface{}{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStreamingRequest(tt.body)
			if got != tt.want {
				t.Errorf("IsStreamingRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
