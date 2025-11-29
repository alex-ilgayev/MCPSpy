package security

import (
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// Re-export types from event package for backward compatibility
type (
	RiskLevel          = event.RiskLevel
	Category           = event.Category
	SecurityAlertEvent = event.SecurityAlertEvent
)

// Re-export constants
const (
	RiskLevelNone     = event.RiskLevelNone
	RiskLevelLow      = event.RiskLevelLow
	RiskLevelMedium   = event.RiskLevelMedium
	RiskLevelHigh     = event.RiskLevelHigh
	RiskLevelCritical = event.RiskLevelCritical

	CategoryBenign    = event.CategoryBenign
	CategoryInjection = event.CategoryInjection
	CategoryJailbreak = event.CategoryJailbreak
	CategoryMalicious = event.CategoryMalicious
)

// ScoreToRiskLevel converts a score to risk level
var ScoreToRiskLevel = event.ScoreToRiskLevel

// DetectionResult is the internal result from the detector
type DetectionResult struct {
	Detected     bool
	RiskLevel    RiskLevel
	RiskScore    float64
	Category     Category
	AnalyzedText string
	Error        string // Set when model is loading or other transient errors
}
