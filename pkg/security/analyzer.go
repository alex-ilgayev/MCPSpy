package security

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/sirupsen/logrus"
)

// Analyzer subscribes to MCP events and performs security analysis
type Analyzer struct {
	config   Config
	detector Detector
	eventBus bus.EventBus

	// Metrics (atomic for thread safety)
	totalAnalyzed atomic.Int64
	totalDetected atomic.Int64
	totalErrors   atomic.Int64
}

// NewAnalyzer creates a new security analyzer
func NewAnalyzer(cfg Config, eventBus bus.EventBus) (*Analyzer, error) {
	detector, err := NewDetector(cfg)
	if err != nil {
		return nil, err
	}

	a := &Analyzer{
		config:   cfg,
		detector: detector,
		eventBus: eventBus,
	}

	// Subscribe to MCP messages
	if err := eventBus.Subscribe(event.EventTypeMCPMessage, a.handleMCPEvent); err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"model":     cfg.Model,
		"threshold": cfg.Threshold,
		"async":     cfg.AsyncMode,
	}).Debug("Security analyzer initialized")

	return a, nil
}

// handleMCPEvent processes incoming MCP events
func (a *Analyzer) handleMCPEvent(e event.Event) {
	mcpEvent, ok := e.(*event.MCPEvent)
	if !ok {
		logrus.Trace("Security analyzer received non-MCP event, skipping")
		return
	}

	logrus.WithFields(logrus.Fields{
		"method":       mcpEvent.Method,
		"message_type": mcpEvent.MessageType,
	}).Trace("Security analyzer received MCP event")

	// Check if we should analyze this method
	if !a.shouldAnalyze(mcpEvent) {
		logrus.WithField("method", mcpEvent.Method).Trace("Security analyzer skipping event (shouldAnalyze=false)")
		return
	}

	// Extract text to analyze
	text := a.extractAnalyzableText(mcpEvent)
	if text == "" {
		logrus.WithField("method", mcpEvent.Method).Trace("Security analyzer skipping event (no analyzable text)")
		return
	}

	// Truncate if needed
	if len(text) > a.config.MaxTextLength {
		text = text[:a.config.MaxTextLength]
		logrus.WithField("max_length", a.config.MaxTextLength).Trace("Truncated text for analysis")
	}

	logrus.WithFields(logrus.Fields{
		"method":      mcpEvent.Method,
		"text_length": len(text),
		"async_mode":  a.config.AsyncMode,
	}).Debug("Starting security analysis")

	if a.config.AsyncMode {
		// Deep copy to avoid data race - the original event may be modified
		// by other subscribers while async analysis is in progress
		eventCopy := mcpEvent.Copy()
		go a.analyze(eventCopy, text)
	} else {
		a.analyze(mcpEvent, text)
	}
}

// shouldAnalyze determines if this event should be analyzed
func (a *Analyzer) shouldAnalyze(e *event.MCPEvent) bool {
	// Skip responses unless configured
	if e.MessageType == event.JSONRPCMessageTypeResponse && !a.config.AnalyzeResponses {
		return false
	}

	// Check high-risk methods filter
	if len(a.config.HighRiskMethodsOnly) > 0 {
		method := e.Method
		// For responses, use the request method
		if e.Request != nil {
			method = e.Request.Method
		}

		for _, m := range a.config.HighRiskMethodsOnly {
			if method == m {
				return true
			}
		}
		return false
	}

	return true
}

// extractAnalyzableText extracts text content to analyze from MCP event
func (a *Analyzer) extractAnalyzableText(e *event.MCPEvent) string {
	var parts []string

	switch e.MessageType {
	case event.JSONRPCMessageTypeRequest, event.JSONRPCMessageTypeNotification:
		// Analyze method + params
		if e.Method != "" {
			parts = append(parts, e.Method)
		}

		if e.Params != nil {
			// For tools/call, focus on arguments
			if e.Method == "tools/call" {
				if args, ok := e.Params["arguments"]; ok {
					if argsJSON, err := json.Marshal(args); err == nil {
						parts = append(parts, string(argsJSON))
					}
				}
				if name, ok := e.Params["name"].(string); ok {
					parts = append(parts, name)
				}
			} else {
				// For other methods, include all params
				if paramsJSON, err := json.Marshal(e.Params); err == nil {
					parts = append(parts, string(paramsJSON))
				}
			}
		}

	case event.JSONRPCMessageTypeResponse:
		// Analyze result content
		if e.Result != nil {
			if resultJSON, err := json.Marshal(e.Result); err == nil {
				parts = append(parts, string(resultJSON))
			}
		}
	}

	return strings.Join(parts, " ")
}

// analyze performs the actual security analysis
func (a *Analyzer) analyze(mcpEvent *event.MCPEvent, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), a.config.Timeout)
	defer cancel()

	logrus.WithFields(logrus.Fields{
		"method":  mcpEvent.Method,
		"timeout": a.config.Timeout,
	}).Trace("Calling detector.Analyze")

	result, err := a.detector.Analyze(ctx, text)
	if err != nil {
		a.totalErrors.Add(1)
		logrus.WithError(err).WithFields(mcpEvent.LogFields()).
			Warn("Security analysis failed")
		return
	}

	a.totalAnalyzed.Add(1)

	logrus.WithFields(logrus.Fields{
		"method":     mcpEvent.Method,
		"detected":   result.Detected,
		"risk_level": result.RiskLevel,
		"risk_score": result.RiskScore,
		"category":   result.Category,
	}).Debug("Security analysis completed")

	// Log and publish if detected
	if result.Detected {
		a.totalDetected.Add(1)

		logrus.WithFields(logrus.Fields{
			"risk_level":    result.RiskLevel,
			"risk_score":    result.RiskScore,
			"category":      result.Category,
			"method":        mcpEvent.Method,
			"analyzed_text": result.AnalyzedText,
		}).Warn("Potential prompt injection detected")

		// Publish security alert event
		alertEvent := &SecurityAlertEvent{
			Timestamp:    time.Now(),
			MCPEvent:     mcpEvent,
			RiskLevel:    result.RiskLevel,
			RiskScore:    result.RiskScore,
			Category:     result.Category,
			AnalyzedText: result.AnalyzedText,
		}
		logrus.Trace("Publishing SecurityAlertEvent to event bus")
		a.eventBus.Publish(alertEvent)
	}
}

// Stats returns current analyzer statistics
func (a *Analyzer) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total_analyzed": a.totalAnalyzed.Load(),
		"total_detected": a.totalDetected.Load(),
		"total_errors":   a.totalErrors.Load(),
	}
}

// Close cleans up resources
func (a *Analyzer) Close() error {
	a.eventBus.Unsubscribe(event.EventTypeMCPMessage, a.handleMCPEvent)
	return a.detector.Close()
}
