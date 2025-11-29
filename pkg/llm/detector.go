package llm

import (
	"regexp"
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// ProviderConfig contains the detection rules for an LLM provider
type ProviderConfig struct {
	Provider       event.LLMProvider
	HostPatterns   []string // Exact match or wildcard patterns
	PathPatterns   []*regexp.Regexp
	RequiredHeader string // Header name that must be present (optional)
}

var providerConfigs = []ProviderConfig{
	{
		Provider: event.ProviderOpenAI,
		HostPatterns: []string{
			"api.openai.com",
		},
		PathPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^/v1/chat/completions$`),
			regexp.MustCompile(`^/v1/completions$`),
			regexp.MustCompile(`^/v1/responses$`),
		},
	},
	{
		Provider: event.ProviderAzure,
		HostPatterns: []string{
			"*.openai.azure.com",
		},
		PathPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^/openai/deployments/[^/]+/chat/completions`),
			regexp.MustCompile(`^/openai/deployments/[^/]+/completions`),
		},
	},
	{
		Provider: event.ProviderAnthropic,
		HostPatterns: []string{
			"api.anthropic.com",
		},
		PathPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^/v1/messages$`),
			regexp.MustCompile(`^/v1/complete$`),
		},
	},
	{
		Provider: event.ProviderGemini,
		HostPatterns: []string{
			"generativelanguage.googleapis.com",
		},
		PathPatterns: []*regexp.Regexp{
			regexp.MustCompile(`:generateContent$`),
			regexp.MustCompile(`:streamGenerateContent$`),
		},
	},
	{
		Provider: event.ProviderOllama,
		HostPatterns: []string{
			"localhost:11434",
			"127.0.0.1:11434",
		},
		PathPatterns: []*regexp.Regexp{
			regexp.MustCompile(`^/api/chat$`),
			regexp.MustCompile(`^/api/generate$`),
			regexp.MustCompile(`^/v1/chat/completions$`), // OpenAI-compatible endpoint
		},
	},
}

// DetectProvider detects the LLM provider based on host and path
func DetectProvider(host, path string, enabledProviders []string) (event.LLMProvider, bool) {
	// Normalize host (remove port for comparison if needed)
	normalizedHost := strings.ToLower(host)

	for _, config := range providerConfigs {
		// Check if this provider is enabled
		if !isProviderEnabled(config.Provider, enabledProviders) {
			continue
		}

		// Check host patterns
		if !matchHost(normalizedHost, config.HostPatterns) {
			continue
		}

		// Check path patterns
		if matchPath(path, config.PathPatterns) {
			return config.Provider, true
		}
	}

	return "", false
}

// matchHost checks if the host matches any of the patterns
func matchHost(host string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchHostPattern(host, pattern) {
			return true
		}
	}
	return false
}

// matchHostPattern checks if a host matches a pattern (supports * wildcard at start)
func matchHostPattern(host, pattern string) bool {
	pattern = strings.ToLower(pattern)

	// Handle wildcard pattern (*.example.com)
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // Remove the "*"
		return strings.HasSuffix(host, suffix)
	}

	// Exact match
	return host == pattern
}

// matchPath checks if the path matches any of the patterns
func matchPath(path string, patterns []*regexp.Regexp) bool {
	// Remove query string from path
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	for _, pattern := range patterns {
		if pattern.MatchString(path) {
			return true
		}
	}
	return false
}

// isProviderEnabled checks if a provider is in the enabled list
func isProviderEnabled(provider event.LLMProvider, enabledProviders []string) bool {
	if len(enabledProviders) == 0 {
		return true // All providers enabled by default
	}

	providerStr := string(provider)
	for _, enabled := range enabledProviders {
		if strings.EqualFold(enabled, providerStr) {
			return true
		}
	}
	return false
}

// IsStreamingRequest checks if the request indicates streaming
func IsStreamingRequest(provider event.LLMProvider, body map[string]interface{}) bool {
	switch provider {
	case event.ProviderOpenAI, event.ProviderAzure, event.ProviderAnthropic, event.ProviderOllama:
		// These providers use "stream": true in the request body
		if stream, ok := body["stream"].(bool); ok {
			return stream
		}
	case event.ProviderGemini:
		// Gemini uses the streamGenerateContent endpoint
		// This is detected via the path, not the body
		return false
	}
	return false
}

// IsStreamingEndpoint checks if the endpoint path indicates streaming
func IsStreamingEndpoint(provider event.LLMProvider, path string) bool {
	switch provider {
	case event.ProviderGemini:
		return strings.Contains(path, "streamGenerateContent")
	}
	return false
}

// GetProviderFromString converts a string to LLMProvider
func GetProviderFromString(s string) (event.LLMProvider, bool) {
	switch strings.ToLower(s) {
	case "openai":
		return event.ProviderOpenAI, true
	case "anthropic", "claude":
		return event.ProviderAnthropic, true
	case "gemini", "google":
		return event.ProviderGemini, true
	case "ollama":
		return event.ProviderOllama, true
	case "azure", "azure_openai":
		return event.ProviderAzure, true
	default:
		return "", false
	}
}

// ValidateProviders validates a list of provider strings
func ValidateProviders(providers []string) []string {
	valid := make([]string, 0, len(providers))
	for _, p := range providers {
		if _, ok := GetProviderFromString(p); ok {
			valid = append(valid, strings.ToLower(p))
		}
	}
	return valid
}
