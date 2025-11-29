package hf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultBaseURL = "https://router.huggingface.co/hf-inference/models"
)

// Result represents the detection result from the HF API
type Result struct {
	TopLabel       string        // The label with highest score
	TopScore       float64       // Score of the top label
	MaliciousScore float64       // Score of malicious/jailbreak/injection labels
	Latency        time.Duration // API call latency
	Error          string        // Error message if any (e.g., model loading)
}

// Client implements the Hugging Face Inference API client
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	model      string
}

// NewClient creates a new HF Inference API client
func NewClient(token, model string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    defaultBaseURL,
		token:      token,
		model:      model,
	}
}

// NewClientWithBaseURL creates a new client with a custom base URL (for testing)
func NewClientWithBaseURL(baseURL, token, model string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		token:      token,
		model:      model,
	}
}

// classifyRequest is the request payload for text classification
type classifyRequest struct {
	Inputs string `json:"inputs"`
}

// labelScore represents a single label and its score
type labelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// errorResponse represents an HF API error response
type errorResponse struct {
	Error         string  `json:"error"`
	EstimatedTime float64 `json:"estimated_time,omitempty"`
}

// Analyze sends text to HF API and returns detection result
func (c *Client) Analyze(ctx context.Context, text string) (*Result, error) {
	start := time.Now()

	// Build request
	reqBody := classifyRequest{Inputs: text}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/%s", c.baseURL, c.model)

	logrus.WithFields(logrus.Fields{
		"url":         url,
		"model":       c.model,
		"text_length": len(text),
	}).Trace("Sending request to HuggingFace API")

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		logrus.WithError(err).WithField("url", url).Debug("HuggingFace API request failed")
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"status_code":   resp.StatusCode,
		"latency":       time.Since(start),
		"response_size": len(body),
	}).Trace("HuggingFace API response received")

	// Handle errors
	if resp.StatusCode != http.StatusOK {
		var errResp errorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			// Model loading - HF warms up models on demand
			if resp.StatusCode == http.StatusServiceUnavailable {
				logrus.WithFields(logrus.Fields{
					"estimated_time": errResp.EstimatedTime,
				}).Debug("HuggingFace model is loading")
				return &Result{
					Latency: time.Since(start),
					Error:   fmt.Sprintf("Model loading, retry in %.0fs", errResp.EstimatedTime),
				}, nil
			}
			logrus.WithFields(logrus.Fields{
				"status_code": resp.StatusCode,
				"error":       errResp.Error,
			}).Debug("HuggingFace API returned error")
			return nil, fmt.Errorf("API error: %s", errResp.Error)
		}
		logrus.WithFields(logrus.Fields{
			"status_code": resp.StatusCode,
			"body":        string(body),
		}).Debug("HuggingFace API returned non-OK status")
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response - HF returns array of arrays: [[{label, score}, ...]]
	var apiResult [][]labelScore
	if err := json.Unmarshal(body, &apiResult); err != nil {
		logrus.WithError(err).WithField("body", string(body)).Debug("Failed to parse HuggingFace response")
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResult) == 0 || len(apiResult[0]) == 0 {
		logrus.WithField("body", string(body)).Debug("Empty response from HuggingFace API")
		return nil, fmt.Errorf("empty response from API")
	}

	// Build result from the labels
	result := c.buildResult(apiResult[0], time.Since(start))

	logrus.WithFields(logrus.Fields{
		"top_label":       result.TopLabel,
		"top_score":       result.TopScore,
		"malicious_score": result.MaliciousScore,
		"latency":         result.Latency,
	}).Debug("HuggingFace analysis result")

	return result, nil
}

// buildResult creates a Result from API response
func (c *Client) buildResult(labels []labelScore, latency time.Duration) *Result {
	// Find the top label and its score
	var topLabel string
	var topScore float64
	var maliciousScore float64

	for _, l := range labels {
		if l.Score > topScore {
			topScore = l.Score
			topLabel = l.Label
		}
		// Track malicious/jailbreak score specifically
		// Different models use different label schemes:
		// - protectai/deberta-v3-base-prompt-injection-v2: INJECTION, SAFE
		// - meta-llama/Llama-Prompt-Guard-2-86M: LABEL_0 (benign), LABEL_1 (jailbreak)
		// - Other models may use: MALICIOUS, JAILBREAK, etc.
		if isMaliciousLabel(l.Label) {
			if l.Score > maliciousScore {
				maliciousScore = l.Score
			}
		}
	}

	return &Result{
		TopLabel:       topLabel,
		TopScore:       topScore,
		MaliciousScore: maliciousScore,
		Latency:        latency,
	}
}

// Close releases any resources (none for HTTP client)
func (c *Client) Close() error {
	return nil
}

// isMaliciousLabel checks if a label indicates malicious/injection content
// Different models use different label schemes
func isMaliciousLabel(label string) bool {
	switch label {
	case "MALICIOUS", "JAILBREAK", "INJECTION":
		// Common labels used by various models
		return true
	case "LABEL_1":
		// meta-llama/Llama-Prompt-Guard-2-86M uses LABEL_1 for jailbreak
		return true
	default:
		return false
	}
}
