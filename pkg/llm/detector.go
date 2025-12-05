package llm

import "strings"

// Provider represents an LLM API provider
type Provider string

const (
	ProviderUnknown   Provider = ""
	ProviderAnthropic Provider = "anthropic"
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

	return ProviderUnknown
}
