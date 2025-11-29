package security

import (
	"context"
	"fmt"

	"github.com/alex-ilgayev/mcpspy/pkg/security/hf"
	"github.com/sirupsen/logrus"
)

const (
	// displayTruncateLength is the maximum length of analyzed text shown in results
	displayTruncateLength = 100
)

// Detector interface for prompt injection detection
type Detector interface {
	// Analyze checks text for injection attempts
	Analyze(ctx context.Context, text string) (*DetectionResult, error)

	// Close releases resources
	Close() error
}

// hfDetector wraps the HuggingFace client and implements Detector
type hfDetector struct {
	client    *hf.Client
	threshold float64
}

// NewDetector creates a detector based on configuration
func NewDetector(cfg Config) (Detector, error) {
	if cfg.HFToken == "" {
		return nil, fmt.Errorf("HuggingFace token is required")
	}

	client := hf.NewClient(
		cfg.HFToken,
		cfg.Model,
		cfg.Timeout,
	)

	return &hfDetector{
		client:    client,
		threshold: cfg.Threshold,
	}, nil
}

// Analyze implements Detector interface
func (d *hfDetector) Analyze(ctx context.Context, text string) (*DetectionResult, error) {
	logrus.WithField("text_length", len(text)).Trace("Detector calling HuggingFace API")

	result, err := d.client.Analyze(ctx, text)
	if err != nil {
		logrus.WithError(err).Debug("HuggingFace API call failed")
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"top_label":       result.TopLabel,
		"top_score":       result.TopScore,
		"malicious_score": result.MaliciousScore,
		"latency":         result.Latency,
		"error":           result.Error,
	}).Trace("HuggingFace API response received")

	return d.convertResult(result, text), nil
}

// convertResult converts hf.Result to DetectionResult
func (d *hfDetector) convertResult(result *hf.Result, text string) *DetectionResult {
	// Handle error case (e.g., model loading)
	if result.Error != "" {
		logrus.WithField("error", result.Error).Debug("HuggingFace returned error (e.g., model loading)")
		return &DetectionResult{
			Detected:  false,
			RiskLevel: RiskLevelNone,
			Error:     result.Error,
		}
	}

	// Determine category from label
	// Different models use different label schemes:
	// - protectai/deberta-v3-base-prompt-injection-v2: INJECTION, SAFE
	// - meta-llama/Llama-Prompt-Guard-2-86M: LABEL_0 (benign), LABEL_1 (jailbreak)
	category := CategoryBenign
	switch result.TopLabel {
	case "MALICIOUS":
		category = CategoryMalicious
	case "JAILBREAK", "LABEL_1":
		// LABEL_1 is used by Llama Prompt Guard 2 for jailbreak detection
		category = CategoryJailbreak
	case "INJECTION":
		category = CategoryInjection
	}

	// Use malicious score for risk assessment
	riskScore := result.MaliciousScore
	if category == CategoryBenign {
		riskScore = 0
	}

	detected := riskScore >= d.threshold && category != CategoryBenign

	// Truncate text for display
	analyzedText := text
	if len(analyzedText) > displayTruncateLength {
		analyzedText = analyzedText[:displayTruncateLength] + "..."
	}

	logrus.WithFields(logrus.Fields{
		"detected":   detected,
		"category":   category,
		"risk_score": riskScore,
		"threshold":  d.threshold,
	}).Trace("Detection result computed")

	return &DetectionResult{
		Detected:     detected,
		RiskLevel:    ScoreToRiskLevel(riskScore),
		RiskScore:    riskScore,
		Category:     category,
		AnalyzedText: analyzedText,
	}
}

// Close implements Detector interface
func (d *hfDetector) Close() error {
	return d.client.Close()
}
