package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/sirupsen/logrus"
)

// JSONLDisplay handles JSONL output formatting
// Subscribes to the following events:
// - EventTypeMCPMessage
// - EventTypeLLMMessage
type JSONLDisplay struct {
	writer   io.Writer
	eventBus bus.EventBus
}

// NewJSONLDisplay creates a new display handler for JSONL output with custom writer
func NewJSONLDisplay(writer io.Writer, eventBus bus.EventBus) (*JSONLDisplay, error) {
	j := &JSONLDisplay{
		writer:   writer,
		eventBus: eventBus,
	}

	// Subscribe to MCP events
	if err := eventBus.Subscribe(event.EventTypeMCPMessage, j.printMessage); err != nil {
		return nil, err
	}

	// Subscribe to security alerts
	if err := eventBus.Subscribe(event.EventTypeSecurityAlert, j.printSecurityAlert); err != nil {
		return nil, err
	}

	// Subscribe to LLM events
	if err := eventBus.Subscribe(event.EventTypeLLMMessage, j.printLLMMessage); err != nil {
		return nil, err
	}

	return j, nil
}

// PrintHeader does nothing for JSONL output (no header needed)
func (j *JSONLDisplay) PrintHeader() {
	// No header for JSONL output
}

// PrintStats does nothing for JSONL output (stats not applicable)
func (j *JSONLDisplay) PrintStats(stats map[string]int) {
	// No stats output for JSONL format
}

// PrintInfo does nothing for JSONL output (info messages not applicable)
func (j *JSONLDisplay) PrintInfo(format string, args ...interface{}) {
	// No info messages for JSONL format
}

// printMessage outputs a single MCP message in JSON format
func (j *JSONLDisplay) printMessage(e event.Event) {
	msg, ok := e.(*event.MCPEvent)
	if !ok {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		logrus.WithError(err).Error("failed to marshal message")
		return
	}

	fmt.Fprintf(j.writer, "%s\n", string(data))
}

// printSecurityAlert outputs a security alert in JSON format
func (j *JSONLDisplay) printSecurityAlert(e event.Event) {
	alert, ok := e.(*event.SecurityAlertEvent)
	if !ok {
		return
	}

	data, err := json.Marshal(alert)
	if err != nil {
		logrus.WithError(err).Error("failed to marshal security alert")
		return
	}

	fmt.Fprintf(j.writer, "%s\n", string(data))
}

// printLLMMessage outputs a single LLM message in JSON format
func (j *JSONLDisplay) printLLMMessage(e event.Event) {
	msg, ok := e.(*event.LLMEvent)
	if !ok {
		return
	}

	// Skip individual stream chunks to reduce noise (optional - can be configurable)
	// For now, we include all messages including chunks for comprehensive logging
	data, err := json.Marshal(msg)
	if err != nil {
		logrus.WithError(err).Error("failed to marshal LLM message")
		return
	}

	fmt.Fprintf(j.writer, "%s\n", string(data))
}
