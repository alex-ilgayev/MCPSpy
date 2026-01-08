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
	// Standard API: /v1beta/models/{MODEL}:generateContent or :streamGenerateContent
	if host == "generativelanguage.googleapis.com" &&
		strings.HasPrefix(path, "/v1beta/models/") &&
		(strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")) {
		return ProviderGemini
	}

	// Gemini CLI (cloudcode): /v1internal:generateContent or :streamGenerateContent
	// Also supports wildcard cloudcode-*.googleapis.com hosts
	if (host == "cloudcode-pa.googleapis.com" || strings.HasPrefix(host, "cloudcode-") && strings.HasSuffix(host, ".googleapis.com")) &&
		(strings.HasSuffix(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")) {
		return ProviderGemini
	}

	return ProviderUnknown
}
