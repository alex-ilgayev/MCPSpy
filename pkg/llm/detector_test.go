package llm

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		name             string
		host             string
		path             string
		enabledProviders []string
		wantProvider     event.LLMProvider
		wantFound        bool
	}{
		// OpenAI tests
		{
			name:         "OpenAI chat completions",
			host:         "api.openai.com",
			path:         "/v1/chat/completions",
			wantProvider: event.ProviderOpenAI,
			wantFound:    true,
		},
		{
			name:         "OpenAI completions legacy",
			host:         "api.openai.com",
			path:         "/v1/completions",
			wantProvider: event.ProviderOpenAI,
			wantFound:    true,
		},
		{
			name:         "OpenAI with query params",
			host:         "api.openai.com",
			path:         "/v1/chat/completions?model=gpt-4",
			wantProvider: event.ProviderOpenAI,
			wantFound:    true,
		},
		{
			name:      "OpenAI wrong path",
			host:      "api.openai.com",
			path:      "/v1/models",
			wantFound: false,
		},

		// Azure OpenAI tests
		{
			name:         "Azure OpenAI deployment",
			host:         "myresource.openai.azure.com",
			path:         "/openai/deployments/gpt-4/chat/completions",
			wantProvider: event.ProviderAzure,
			wantFound:    true,
		},
		{
			name:         "Azure OpenAI with api version",
			host:         "company.openai.azure.com",
			path:         "/openai/deployments/my-model/completions?api-version=2024-02-01",
			wantProvider: event.ProviderAzure,
			wantFound:    true,
		},

		// Anthropic tests
		{
			name:         "Anthropic messages",
			host:         "api.anthropic.com",
			path:         "/v1/messages",
			wantProvider: event.ProviderAnthropic,
			wantFound:    true,
		},
		{
			name:         "Anthropic complete legacy",
			host:         "api.anthropic.com",
			path:         "/v1/complete",
			wantProvider: event.ProviderAnthropic,
			wantFound:    true,
		},

		// Gemini tests
		{
			name:         "Gemini generateContent",
			host:         "generativelanguage.googleapis.com",
			path:         "/v1beta/models/gemini-pro:generateContent",
			wantProvider: event.ProviderGemini,
			wantFound:    true,
		},
		{
			name:         "Gemini streamGenerateContent",
			host:         "generativelanguage.googleapis.com",
			path:         "/v1/models/gemini-1.5-flash:streamGenerateContent",
			wantProvider: event.ProviderGemini,
			wantFound:    true,
		},

		// Ollama tests
		{
			name:         "Ollama chat localhost",
			host:         "localhost:11434",
			path:         "/api/chat",
			wantProvider: event.ProviderOllama,
			wantFound:    true,
		},
		{
			name:         "Ollama generate 127.0.0.1",
			host:         "127.0.0.1:11434",
			path:         "/api/generate",
			wantProvider: event.ProviderOllama,
			wantFound:    true,
		},
		{
			name:         "Ollama OpenAI compatible",
			host:         "localhost:11434",
			path:         "/v1/chat/completions",
			wantProvider: event.ProviderOllama,
			wantFound:    true,
		},

		// Provider filtering tests
		{
			name:             "OpenAI filtered out",
			host:             "api.openai.com",
			path:             "/v1/chat/completions",
			enabledProviders: []string{"anthropic"},
			wantFound:        false,
		},
		{
			name:             "Only OpenAI enabled",
			host:             "api.openai.com",
			path:             "/v1/chat/completions",
			enabledProviders: []string{"openai"},
			wantProvider:     event.ProviderOpenAI,
			wantFound:        true,
		},

		// Negative tests
		{
			name:      "Unknown host",
			host:      "api.unknown.com",
			path:      "/v1/chat/completions",
			wantFound: false,
		},
		{
			name:      "Empty host",
			host:      "",
			path:      "/v1/chat/completions",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotFound := DetectProvider(tt.host, tt.path, tt.enabledProviders)
			if gotFound != tt.wantFound {
				t.Errorf("DetectProvider() found = %v, want %v", gotFound, tt.wantFound)
			}
			if gotFound && gotProvider != tt.wantProvider {
				t.Errorf("DetectProvider() provider = %v, want %v", gotProvider, tt.wantProvider)
			}
		})
	}
}

func TestMatchHostPattern(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		pattern string
		want    bool
	}{
		{
			name:    "exact match",
			host:    "api.openai.com",
			pattern: "api.openai.com",
			want:    true,
		},
		{
			name:    "wildcard match",
			host:    "myresource.openai.azure.com",
			pattern: "*.openai.azure.com",
			want:    true,
		},
		{
			name:    "wildcard no match",
			host:    "openai.azure.com",
			pattern: "*.openai.azure.com",
			want:    false,
		},
		{
			name:    "case insensitive pattern",
			host:    "api.openai.com", // host is normalized before calling matchHostPattern
			pattern: "API.OPENAI.COM", // pattern is lowercased internally
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchHostPattern(tt.host, tt.pattern)
			if got != tt.want {
				t.Errorf("matchHostPattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStreamingRequest(t *testing.T) {
	tests := []struct {
		name     string
		provider event.LLMProvider
		body     map[string]interface{}
		want     bool
	}{
		{
			name:     "OpenAI streaming true",
			provider: event.ProviderOpenAI,
			body:     map[string]interface{}{"stream": true},
			want:     true,
		},
		{
			name:     "OpenAI streaming false",
			provider: event.ProviderOpenAI,
			body:     map[string]interface{}{"stream": false},
			want:     false,
		},
		{
			name:     "OpenAI no stream field",
			provider: event.ProviderOpenAI,
			body:     map[string]interface{}{"model": "gpt-4"},
			want:     false,
		},
		{
			name:     "Anthropic streaming true",
			provider: event.ProviderAnthropic,
			body:     map[string]interface{}{"stream": true},
			want:     true,
		},
		{
			name:     "Gemini always false from body",
			provider: event.ProviderGemini,
			body:     map[string]interface{}{"stream": true},
			want:     false, // Gemini uses endpoint, not body
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStreamingRequest(tt.provider, tt.body)
			if got != tt.want {
				t.Errorf("IsStreamingRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStreamingEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		provider event.LLMProvider
		path     string
		want     bool
	}{
		{
			name:     "Gemini stream endpoint",
			provider: event.ProviderGemini,
			path:     "/v1/models/gemini-pro:streamGenerateContent",
			want:     true,
		},
		{
			name:     "Gemini non-stream endpoint",
			provider: event.ProviderGemini,
			path:     "/v1/models/gemini-pro:generateContent",
			want:     false,
		},
		{
			name:     "OpenAI always false",
			provider: event.ProviderOpenAI,
			path:     "/v1/chat/completions",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsStreamingEndpoint(tt.provider, tt.path)
			if got != tt.want {
				t.Errorf("IsStreamingEndpoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetProviderFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     event.LLMProvider
		wantOk   bool
	}{
		{"openai", "openai", event.ProviderOpenAI, true},
		{"OpenAI caps", "OpenAI", event.ProviderOpenAI, true},
		{"anthropic", "anthropic", event.ProviderAnthropic, true},
		{"claude alias", "claude", event.ProviderAnthropic, true},
		{"gemini", "gemini", event.ProviderGemini, true},
		{"google alias", "google", event.ProviderGemini, true},
		{"ollama", "ollama", event.ProviderOllama, true},
		{"azure", "azure", event.ProviderAzure, true},
		{"azure_openai", "azure_openai", event.ProviderAzure, true},
		{"unknown", "unknown", "", false},
		{"empty", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GetProviderFromString(tt.input)
			if ok != tt.wantOk {
				t.Errorf("GetProviderFromString() ok = %v, wantOk %v", ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("GetProviderFromString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateProviders(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  int
	}{
		{"all valid", []string{"openai", "anthropic"}, 2},
		{"some invalid", []string{"openai", "invalid", "anthropic"}, 2},
		{"all invalid", []string{"invalid1", "invalid2"}, 0},
		{"empty", []string{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateProviders(tt.input)
			if len(got) != tt.want {
				t.Errorf("ValidateProviders() returned %d providers, want %d", len(got), tt.want)
			}
		})
	}
}
