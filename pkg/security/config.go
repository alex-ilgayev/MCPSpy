package security

import "time"

// Config holds security analyzer configuration
type Config struct {
	// Enabled determines if security analysis is active
	Enabled bool

	// HFToken is the Hugging Face API token
	HFToken string

	// Model to use (default: meta-llama/Llama-Prompt-Guard-2-86M)
	Model string

	// Threshold for detection (default: 0.5)
	// Messages with score >= threshold are flagged
	Threshold float64

	// Timeout for API calls (default: 10s)
	Timeout time.Duration

	// AsyncMode runs analysis without blocking message flow
	AsyncMode bool

	// AnalyzeResponses also analyzes response content (default: false)
	// Useful for detecting injection in tool outputs
	AnalyzeResponses bool

	// MaxTextLength truncates text before sending to API (default: 4096)
	// Prompt Guard 2 supports 512 tokens (~2000 chars), but we allow more
	MaxTextLength int

	// HighRiskMethodsOnly only analyzes these methods (default: high-risk methods)
	// Example: []string{"tools/call", "resources/read", "prompts/get"}
	HighRiskMethodsOnly []string
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Enabled:          false,
		Model:            "meta-llama/Llama-Prompt-Guard-2-86M",
		Threshold:        0.5,
		Timeout:          10 * time.Second,
		AsyncMode:        true,
		AnalyzeResponses: false,
		MaxTextLength:    4096,
		HighRiskMethodsOnly: []string{
			"tools/call",
			"resources/read",
			"prompts/get",
			"completion/complete",
			"sampling/createMessage",
		},
	}
}
