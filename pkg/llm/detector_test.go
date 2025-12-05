package llm

import "testing"

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
		want Provider
	}{
		{
			name: "valid anthropic messages endpoint",
			host: "api.anthropic.com",
			path: "/v1/messages",
			want: ProviderAnthropic,
		},
		{
			name: "anthropic with query params",
			host: "api.anthropic.com",
			path: "/v1/messages?version=2023-06-01",
			want: ProviderAnthropic,
		},
		{
			name: "anthropic case insensitive host",
			host: "API.ANTHROPIC.COM",
			path: "/v1/messages",
			want: ProviderAnthropic,
		},
		{
			name: "wrong host",
			host: "api.openai.com",
			path: "/v1/messages",
			want: ProviderUnknown,
		},
		{
			name: "wrong path",
			host: "api.anthropic.com",
			path: "/v1/complete",
			want: ProviderUnknown,
		},
		{
			name: "empty host",
			host: "",
			path: "/v1/messages",
			want: ProviderUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectProvider(tt.host, tt.path)
			if got != tt.want {
				t.Errorf("DetectProvider() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLLMRequest(t *testing.T) {
	tests := []struct {
		name string
		host string
		path string
		want bool
	}{
		{
			name: "anthropic is LLM",
			host: "api.anthropic.com",
			path: "/v1/messages",
			want: true,
		},
		{
			name: "unknown is not LLM",
			host: "example.com",
			path: "/api",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectProvider(tt.host, tt.path) != ProviderUnknown
			if got != tt.want {
				t.Errorf("IsLLMRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
