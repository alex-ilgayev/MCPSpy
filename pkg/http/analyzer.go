package http

import (
	"encoding/json"
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
	"github.com/sirupsen/logrus"
)

// Analyzer is a simple POC HTTP analyzer
type Analyzer struct {
}

// NewAnalyzer creates a new HTTP analyzer
func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

// AnalyzeEvent analyzes an HTTP event and logs interesting information
func (a *Analyzer) AnalyzeEvent(event *ebpf.HTTPPayload) {
	if event.IsRequest {
		a.analyzeRequest(event)
	} else {
		a.analyzeResponse(event)
	}
}

// analyzeRequest analyzes HTTP requests
func (a *Analyzer) analyzeRequest(event *ebpf.HTTPPayload) {
	entry := logrus.WithFields(logrus.Fields{
		"type":   "http_request",
		"method": event.Method,
		"url":    event.URL,
		"pid":    event.PID,
	})

	// Log basic request info
	entry.Info("HTTP Request captured")

	// Check for interesting headers
	if userAgent, ok := event.Headers["user-agent"]; ok {
		entry.WithField("user_agent", userAgent).Debug("User-Agent detected")
	}

	if contentType, ok := event.Headers["content-type"]; ok {
		entry.WithField("content_type", contentType).Debug("Content-Type detected")
	}

	// Check if it's a JSON request
	if a.isJSON(event.Headers["content-type"]) && len(event.Body) > 0 {
		a.analyzeJSONBody(entry, event.Body)
	}

	// Check for API patterns
	if strings.Contains(event.URL, "/api/") {
		entry.Info("API endpoint detected")
	}

	// Check for authentication headers
	if _, hasAuth := event.Headers["authorization"]; hasAuth {
		entry.Info("Authorization header present")
	}
}

// analyzeResponse analyzes HTTP responses
func (a *Analyzer) analyzeResponse(event *ebpf.HTTPPayload) {
	entry := logrus.WithFields(logrus.Fields{
		"type":        "http_response",
		"status_code": event.StatusCode,
		"pid":         event.PID,
	})

	// Log basic response info
	entry.Info("HTTP Response captured")

	// Check status code categories
	switch {
	case event.StatusCode >= 200 && event.StatusCode < 300:
		entry.Debug("Success response")
	case event.StatusCode >= 300 && event.StatusCode < 400:
		entry.Debug("Redirect response")
	case event.StatusCode >= 400 && event.StatusCode < 500:
		entry.Warn("Client error response")
	case event.StatusCode >= 500:
		entry.Error("Server error response")
	}

	// Check if it's a JSON response
	if a.isJSON(event.Headers["content-type"]) && len(event.Body) > 0 {
		a.analyzeJSONBody(entry, event.Body)
	}

	// Check for interesting response headers
	if serverHeader, ok := event.Headers["server"]; ok {
		entry.WithField("server", serverHeader).Debug("Server header detected")
	}
}

// isJSON checks if the content type indicates JSON
func (a *Analyzer) isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

// analyzeJSONBody attempts to parse and analyze JSON body
func (a *Analyzer) analyzeJSONBody(entry *logrus.Entry, body []byte) {
	var jsonData interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		entry.WithError(err).Debug("Failed to parse JSON body")
		return
	}

	entry.WithField("json_body", jsonData).Debug("JSON body parsed")

	// Check for MCP-like patterns in JSON
	if jsonMap, ok := jsonData.(map[string]interface{}); ok {
		// Check for JSON-RPC 2.0 pattern (used by MCP)
		if jsonrpc, ok := jsonMap["jsonrpc"]; ok && jsonrpc == "2.0" {
			entry.Info("JSON-RPC 2.0 message detected (possible MCP traffic)")

			// Log method if present
			if method, ok := jsonMap["method"].(string); ok {
				entry.WithField("jsonrpc_method", method).Info("MCP method detected")
			}

			// Log id if present
			if id, ok := jsonMap["id"]; ok {
				entry.WithField("jsonrpc_id", id).Debug("Request/Response ID")
			}
		}

		// Check for other interesting patterns
		a.checkForInterestingPatterns(entry, jsonMap)
	}
}

// checkForInterestingPatterns looks for interesting patterns in JSON data
func (a *Analyzer) checkForInterestingPatterns(entry *logrus.Entry, data map[string]interface{}) {
	// Look for API keys or tokens
	for key := range data {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "token") ||
			strings.Contains(lowerKey, "key") ||
			strings.Contains(lowerKey, "secret") ||
			strings.Contains(lowerKey, "password") {
			entry.WithField("sensitive_field", key).Warn("Potential sensitive data field detected")
		}

		// Check for nested MCP-like structures
		if lowerKey == "params" || lowerKey == "result" {
			entry.Debug("MCP-like structure detected")
		}
	}
}

// GetSummary returns a summary of analyzed HTTP traffic
func (a *Analyzer) GetSummary() string {
	// This is a POC - in a real implementation, you would track statistics
	return "HTTP Analyzer POC - extend this to track statistics and patterns"
}
