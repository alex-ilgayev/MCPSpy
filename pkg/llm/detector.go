package llm

import "strings"

// IsAnthropicRequest checks if the request is to the Anthropic API
func IsAnthropicRequest(host, path string) bool {
	host = strings.ToLower(host)

	// Check host
	if host != "api.anthropic.com" {
		return false
	}

	// Check path (remove query string)
	if idx := strings.Index(path, "?"); idx != -1 {
		path = path[:idx]
	}

	return path == "/v1/messages"
}

// IsStreamingRequest checks if the request body indicates streaming
func IsStreamingRequest(body map[string]interface{}) bool {
	if stream, ok := body["stream"].(bool); ok {
		return stream
	}
	return false
}
