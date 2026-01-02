package llm

import "strings"

// Provider represents an LLM API provider
type Provider string

const (
	ProviderUnknown   Provider = ""
	ProviderAnthropic Provider = "anthropic"
	ProviderGemini    Provider = "gemini"
)

// DetectProvider detects the LLM provider from HTTP request parameters
func DetectProvider(host, path string) Provider {
	host = strings.ToLower(host)

	// Remove query string from path
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	// Anthropic detection
	if host == "api.anthropic.com" && path == "/v1/messages" {
		return ProviderAnthropic
	}

	// Gemini detection
	// Endpoints: /v1beta/models/{MODEL}:generateContent or :streamGenerateContent
	if host == "generativelanguage.googleapis.com" &&
		strings.HasPrefix(path, "/v1beta/models/") &&
		(strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")) {
		return ProviderGemini
	}

	return ProviderUnknown
}
