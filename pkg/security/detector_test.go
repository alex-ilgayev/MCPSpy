package security

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScoreToRiskLevel(t *testing.T) {
	tests := []struct {
		score    float64
		expected RiskLevel
	}{
		{0.0, RiskLevelNone},
		{0.1, RiskLevelNone},
		{0.29, RiskLevelNone},
		{0.3, RiskLevelLow},
		{0.4, RiskLevelLow},
		{0.5, RiskLevelMedium},
		{0.6, RiskLevelMedium},
		{0.7, RiskLevelHigh},
		{0.8, RiskLevelHigh},
		{0.9, RiskLevelCritical},
		{1.0, RiskLevelCritical},
	}

	for _, tt := range tests {
		result := ScoreToRiskLevel(tt.score)
		assert.Equal(t, tt.expected, result, "ScoreToRiskLevel(%f)", tt.score)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.False(t, cfg.Enabled, "expected Enabled to be false by default")
	assert.Equal(t, "meta-llama/Llama-Prompt-Guard-2-86M", cfg.Model)
	assert.Equal(t, 0.5, cfg.Threshold)
	assert.Equal(t, 10*time.Second, cfg.Timeout)
	assert.True(t, cfg.AsyncMode, "expected AsyncMode to be true by default")
	assert.Equal(t, 4096, cfg.MaxTextLength)
	assert.NotEmpty(t, cfg.HighRiskMethodsOnly, "expected HighRiskMethodsOnly to have default values")
}

func TestNewDetector_MissingToken(t *testing.T) {
	cfg := Config{
		Enabled: true,
		HFToken: "",
	}

	_, err := NewDetector(cfg)
	require.Error(t, err, "expected error for missing HF token")
}

func TestNewDetector_WithToken(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		HFToken:   "test-token",
		Model:     "test-model",
		Threshold: 0.5,
		Timeout:   5 * time.Second,
	}

	detector, err := NewDetector(cfg)
	require.NoError(t, err)
	require.NotNil(t, detector, "expected detector to be created")
	detector.Close()
}
