package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/sirupsen/logrus"
)

// ConsoleDisplay handles the CLI output formatting for console output
// Subscribes to the following events:
// - EventTypeMCPMessage
type ConsoleDisplay struct {
	writer      io.Writer
	showBuffers bool
	eventBus    bus.EventBus
}

// NewConsoleDisplay creates a new display handler for console output with custom writer
func NewConsoleDisplay(writer io.Writer, showBuffers bool, eventBus bus.EventBus) (*ConsoleDisplay, error) {
	d := &ConsoleDisplay{
		writer:      writer,
		showBuffers: showBuffers,
		eventBus:    eventBus,
	}

	// Subscribe to MCP events
	if err := eventBus.Subscribe(event.EventTypeMCPMessage, d.printMessage); err != nil {
		return nil, err
	}

	return d, nil
}

// Colors for different elements
var (
	timestampColor = color.New(color.FgHiBlack)
	transportColor = color.New(color.FgHiCyan)
	pidColor       = color.New(color.FgCyan)
	commColor      = color.New(color.FgYellow)
	methodColor    = color.New(color.FgGreen)
	errorColor     = color.New(color.FgRed)
	errorCodeColor = color.New(color.FgHiRed)
	headerColor    = color.New(color.FgWhite, color.Bold)
	idColor        = color.New(color.FgHiBlack)
)

// PrintHeader prints the MCPSpy header
func (d *ConsoleDisplay) PrintHeader() {
	header := `
███╗   ███╗ ██████╗██████╗ ███████╗██████╗ ██╗   ██╗
████╗ ████║██╔════╝██╔══██╗██╔════╝██╔══██╗╚██╗ ██╔╝
██╔████╔██║██║     ██████╔╝███████╗██████╔╝ ╚████╔╝ 
██║╚██╔╝██║██║     ██╔═══╝ ╚════██║██╔═══╝   ╚██╔╝  
██║ ╚═╝ ██║╚██████╗██║     ███████║██║        ██║   
╚═╝     ╚═╝ ╚═════╝╚═╝     ╚══════╝╚═╝        ╚═╝   
`
	headerColor.Fprintln(d.writer, header)
	fmt.Fprintln(d.writer, "MCP Protocol Spy - Monitoring Model Context Protocol Communication")
	fmt.Fprintln(d.writer, strings.Repeat("─", 80))
}

// PrintStats prints statistics table
func (d *ConsoleDisplay) PrintStats(stats map[string]int) {
	fmt.Fprintln(d.writer, "\n"+strings.Repeat("─", 80))
	headerColor.Fprintln(d.writer, "Statistics:")

	table := tablewriter.NewWriter(d.writer)
	table.SetHeader([]string{"Method", "Count"})
	table.SetBorder(false)
	table.SetColumnSeparator("│")
	table.SetRowSeparator("─")
	table.SetHeaderLine(true)

	for method, count := range stats {
		table.Append([]string{method, fmt.Sprintf("%d", count)})
	}

	table.Render()
}

// PrintInfo prints an info message
func (d *ConsoleDisplay) PrintInfo(format string, args ...interface{}) {
	fmt.Fprintf(d.writer, format+"\n", args...)
}

// printCorrelatedMessage prints a correlated message showing transport communication
func (d *ConsoleDisplay) printMessage(e event.Event) {
	msg, ok := e.(*event.MCPEvent)
	if !ok {
		return
	}

	// Format timestamp
	ts := timestampColor.Sprint(msg.Timestamp.Format("15:04:05.000"))
	fmt.Fprintf(d.writer, "%s ", ts)

	// Format the communication flow based on transport type
	d.printCommFlow(msg)

	// Format message type and method
	d.printMessageInfo(msg)

	// Print a new line after the message info
	fmt.Fprintln(d.writer)

	// Print buffer content if requested
	if d.showBuffers && msg.Raw != "" {
		d.printBuffer(msg.Raw)
	}
}

// printCommFlow formats the communication flow for a given message
// Format: [transport] [from] → [to]
func (d *ConsoleDisplay) printCommFlow(msg *event.MCPEvent) {
	var commFlow string

	switch msg.TransportType {
	case event.TransportTypeStdio:
		if msg.StdioTransport != nil {
			commFlow = fmt.Sprintf("%s %s[%s] → %s[%s]",
				transportColor.Sprint("STDIO"),
				commColor.Sprint(msg.FromComm),
				pidColor.Sprint(msg.FromPID),
				commColor.Sprint(msg.ToComm),
				pidColor.Sprint(msg.ToPID),
			)
		} else {
			logrus.Warnf("unknown stdio transport: %v", msg.StdioTransport)
			commFlow = transportColor.Sprint("UNKN")
		}
	case event.TransportTypeHTTP:
		if msg.HttpTransport != nil {
			if msg.HttpTransport.IsRequest {
				commFlow = fmt.Sprintf("%s %s[%s] → %s",
					transportColor.Sprint("HTTP"),
					commColor.Sprint(msg.HttpTransport.Comm),
					pidColor.Sprint(msg.HttpTransport.PID),
					commColor.Sprint(msg.HttpTransport.Host),
				)
			} else {
				commFlow = fmt.Sprintf("%s %s → %s[%s]",
					transportColor.Sprint("HTTP"),
					commColor.Sprint(msg.HttpTransport.Host),
					commColor.Sprint(msg.HttpTransport.Comm),
					pidColor.Sprint(msg.HttpTransport.PID),
				)
			}
		} else {
			logrus.Warnf("unknown http transport: %v", msg.HttpTransport)
			commFlow = transportColor.Sprint("UNKN")
		}
	default:
		logrus.Warnf("unknown transport type: %v", msg.TransportType)
		commFlow = transportColor.Sprint("UNKN")
	}

	fmt.Fprintf(d.writer, "%s ", commFlow)
}

// printMessageInfo formats the message info for a given message
// Format: [id] [type] [method]
func (d *ConsoleDisplay) printMessageInfo(msg *event.MCPEvent) {
	var msgInfo string
	switch msg.MessageType {
	case event.JSONRPCMessageTypeRequest:
		msgInfo = fmt.Sprintf("%s REQ  %s", idColor.Sprint(fmt.Sprintf("[%v]", msg.ID)), methodColor.Sprint(msg.Method))
		switch msg.Method {
		case "tools/call":
			if toolName := msg.ExtractToolName(); toolName != "" {
				msgInfo += fmt.Sprintf(" (%s)", toolName)
			}
		case "resources/read":
			if uri := msg.ExtractResourceURI(); uri != "" {
				msgInfo += fmt.Sprintf(" (%s)", uri)
			}
		}
	case event.JSONRPCMessageTypeResponse:
		if msg.Error.Message != "" {
			msgInfo = fmt.Sprintf("%s ERR  %s %s", idColor.Sprint(fmt.Sprintf("[%v]", msg.ID)), errorColor.Sprint(msg.Error.Message), errorCodeColor.Sprintf("(Code: %d)", msg.Error.Code))
		} else {
			msgInfo = fmt.Sprintf("%s RESP OK", idColor.Sprint(fmt.Sprintf("[%v]", msg.ID)))
		}
	case event.JSONRPCMessageTypeNotification:
		msgInfo = fmt.Sprintf("%s NOTF %s", idColor.Sprint("[-]"), methodColor.Sprint(msg.Method))
	default:
		msgInfo = "UNKN"
	}

	if msg.MessageType != event.JSONRPCMessageTypeResponse {
		msgInfo += fmt.Sprintf(" %s", mcp.GetMethodDescription(msg.Method))
	}

	fmt.Fprintf(d.writer, "%s ", msgInfo)
}

// printBuffer prints the raw message content with proper JSON formatting
func (d *ConsoleDisplay) printBuffer(content string) {
	// Try to parse and pretty-print JSON
	var prettyContent string
	var jsonObj interface{}

	if err := json.Unmarshal([]byte(content), &jsonObj); err == nil {
		// Valid JSON - pretty print it
		if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
			prettyContent = string(prettyBytes)
		} else {
			prettyContent = content
		}
	} else {
		// Not valid JSON - use as-is
		prettyContent = content
	}

	// Split into lines and print with consistent formatting
	lines := strings.Split(prettyContent, "\n")

	// Print top border
	fmt.Fprintln(d.writer, "┌────")

	// Print content lines
	for _, line := range lines {
		if line != "" {
			fmt.Fprintf(d.writer, "│ %s\n", line)
		}
	}

	// Print bottom border
	fmt.Fprintln(d.writer, "└────")
}
