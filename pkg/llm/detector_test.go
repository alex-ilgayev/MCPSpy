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
		// Gemini test cases
		{
			name: "valid gemini generateContent endpoint",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			want: ProviderGemini,
		},
		{
			name: "gemini streamGenerateContent endpoint",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-1.5-pro:streamGenerateContent",
			want: ProviderGemini,
		},
		{
			name: "gemini with alt=sse query param",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
			want: ProviderGemini,
		},
		{
			name: "gemini with API key query param",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-2.0-flash:generateContent?key=AIzaSy...",
			want: ProviderGemini,
		},
		{
			name: "gemini case insensitive host",
			host: "GENERATIVELANGUAGE.GOOGLEAPIS.COM",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			want: ProviderGemini,
		},
		{
			name: "gemini wrong endpoint (no colon action)",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-2.0-flash",
			want: ProviderUnknown,
		},
		{
			name: "gemini wrong path prefix",
			host: "generativelanguage.googleapis.com",
			path: "/v1/models/gemini-2.0-flash:generateContent",
			want: ProviderUnknown,
		},
		// OpenAI test cases
		{
			name: "valid openai chat completions endpoint",
			host: "api.openai.com",
			path: "/v1/chat/completions",
			want: ProviderOpenAI,
		},
		{
			name: "openai case insensitive host",
			host: "API.OPENAI.COM",
			path: "/v1/chat/completions",
			want: ProviderOpenAI,
		},
		{
			name: "openai wrong endpoint",
			host: "api.openai.com",
			path: "/v1/completions",
			want: ProviderUnknown,
		},
		{
			name: "openai wrong host",
			host: "api.azure.com",
			path: "/v1/chat/completions",
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
		{
			name: "gemini is LLM",
			host: "generativelanguage.googleapis.com",
			path: "/v1beta/models/gemini-2.0-flash:generateContent",
			want: true,
		},
		{
			name: "openai is LLM",
			host: "api.openai.com",
			path: "/v1/chat/completions",
			want: true,
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
