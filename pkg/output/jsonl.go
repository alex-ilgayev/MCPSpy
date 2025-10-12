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

// printMessage outputs a single message in JSON format
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
