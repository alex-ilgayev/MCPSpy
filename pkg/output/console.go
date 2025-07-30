package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/alex-ilgayev/mcpspy/pkg/mcp"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
)

// ConsoleDisplay handles the CLI output formatting for console output
type ConsoleDisplay struct {
	writer      io.Writer
	showBuffers bool
}

// NewConsoleDisplay creates a new display handler for console output with custom writer
func NewConsoleDisplay(writer io.Writer, showBuffers bool) *ConsoleDisplay {
	return &ConsoleDisplay{
		writer:      writer,
		showBuffers: showBuffers,
	}
}

// Colors for different elements
var (
	timestampColor = color.New(color.FgHiBlack)
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

// PrintMessages prints MCP messages, handling both regular and correlated messages
func (d *ConsoleDisplay) PrintMessages(messages []*mcp.Message) {
	for _, msg := range messages {
		d.printMessage(msg)
	}
}

// printCorrelatedMessage prints a correlated message showing process-to-process communication
func (d *ConsoleDisplay) printMessage(msg *mcp.Message) {
	// Format timestamp
	ts := timestampColor.Sprint(msg.Timestamp.Format("15:04:05.000"))

	// Format the communication flow
	commFlow := fmt.Sprintf("%s[%s] → %s[%s]",
		commColor.Sprint(msg.FromComm),
		pidColor.Sprint(msg.FromPID),
		commColor.Sprint(msg.ToComm),
		pidColor.Sprint(msg.ToPID),
	)

	// Format message type and method
	var msgInfo string
	switch msg.Type {
	case mcp.JSONRPCMessageTypeRequest:
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
	case mcp.JSONRPCMessageTypeResponse:
		if msg.Error.Message != "" {
			msgInfo = fmt.Sprintf("%s ERR  %s %s", idColor.Sprint(fmt.Sprintf("[%v]", msg.ID)), errorColor.Sprint(msg.Error.Message), errorCodeColor.Sprintf("(Code: %d)", msg.Error.Code))
		} else {
			msgInfo = fmt.Sprintf("%s RESP OK", idColor.Sprint(fmt.Sprintf("[%v]", msg.ID)))
		}
	case mcp.JSONRPCMessageTypeNotification:
		msgInfo = fmt.Sprintf("%s NOTF %s", idColor.Sprint("[-]"), methodColor.Sprint(msg.Method))
	default:
		msgInfo = "UNKN"
	}

	// Print the main line with process-to-process communication
	fmt.Fprintf(d.writer, "%s %s %s",
		ts,
		commFlow,
		msgInfo,
	)
	if msg.Type != mcp.JSONRPCMessageTypeResponse {
		fmt.Fprintf(d.writer, " %s\n", mcp.GetMethodDescription(msg.Method))
	} else {
		fmt.Fprintln(d.writer)
	}

	// Print buffer content if requested
	if d.showBuffers && msg.Raw != "" {
		d.printBuffer(msg.Raw)
	}
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
