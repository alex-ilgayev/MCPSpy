package event

import (
	"time"

	"github.com/sirupsen/logrus"
)

// RiskLevel represents the severity of detected injection
type RiskLevel string

const (
	RiskLevelNone     RiskLevel = "none"
	RiskLevelLow      RiskLevel = "low"      // score [0.3, 0.5)
	RiskLevelMedium   RiskLevel = "medium"   // score [0.5, 0.7)
	RiskLevelHigh     RiskLevel = "high"     // score [0.7, 0.9)
	RiskLevelCritical RiskLevel = "critical" // score >= 0.9
)

// Category represents the type of detected threat
type Category string

const (
	CategoryBenign    Category = "benign"
	CategoryInjection Category = "injection" // Prompt Guard v1 only
	CategoryJailbreak Category = "jailbreak"
	CategoryMalicious Category = "malicious" // Prompt Guard v2
)

// SecurityAlertEvent is published when injection is detected
type SecurityAlertEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	MCPEvent     *MCPEvent `json:"mcp_event"`
	RiskLevel    RiskLevel `json:"risk_level"`
	RiskScore    float64   `json:"risk_score"` // 0.0 - 1.0
	Category     Category  `json:"category"`
	AnalyzedText string    `json:"analyzed_text"` // Truncated for display
}

func (e *SecurityAlertEvent) Type() EventType {
	return EventTypeSecurityAlert
}

func (e *SecurityAlertEvent) LogFields() logrus.Fields {
	return logrus.Fields{
		"risk_level": e.RiskLevel,
		"risk_score": e.RiskScore,
		"category":   e.Category,
		"method":     e.MCPEvent.Method,
	}
}

// ScoreToRiskLevel converts a score to risk level
func ScoreToRiskLevel(score float64) RiskLevel {
	switch {
	case score >= 0.9:
		return RiskLevelCritical
	case score >= 0.7:
		return RiskLevelHigh
	case score >= 0.5:
		return RiskLevelMedium
	case score >= 0.3:
		return RiskLevelLow
	default:
		return RiskLevelNone
	}
}
