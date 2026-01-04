package output

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/alex-ilgayev/mcpspy/pkg/version"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxMessages = 1000
)

// messageSource distinguishes between MCP and LLM events (internal to TUI)
type messageSource int

const (
	sourceTypeMCP messageSource = iota
	sourceTypeLLM
)

// displayMessage wraps either MCP or LLM events for rendering
type displayMessage struct {
	source   messageSource
	mcpEvent *event.MCPEvent
	llmEvent *event.LLMEvent
}

// TUIDisplay handles the TUI output using Bubbletea
type TUIDisplay struct {
	program  *tea.Program
	model    *model
	eventBus bus.EventBus
}

type viewMode int

const (
	viewModeMain viewMode = iota
	viewModeDetail
)

// densityMode defines the display density
type densityMode int

const (
	densityComfort densityMode = iota
	densityCompact
	densityUltra
)

// model is the Bubbletea model for the TUI
type model struct {
	messages          []*displayMessage
	selectedIndex     int
	scrollOffset      int
	paused            bool
	viewMode          viewMode
	prettyJSON        bool
	detailScroll      int
	width             int
	height            int
	autoScroll        bool
	stats             map[string]int
	bannerCollapsed   bool
	searchQuery       string
	searchResults     []int
	currentSearchIdx  int
	filterTransport   string // "ALL", "HTTP", "STDIO"
	filterType        string // "ALL", "REQ", "RESP", "NOTIFY", "ERROR"
	filterApp         string // "ALL", "MCP", "LLM"
	jsonWrap          bool
	density           densityMode
	requestToResponse map[string]*event.MCPEvent // Maps request key to response message
	detailViewTab     string                     // "request" or "response"
}

// Bubbletea message types
type msgReceived struct {
	msg *displayMessage
}

type tickMsg time.Time

// NewTUIDisplay creates a new TUI display handler
func NewTUIDisplay(eventBus bus.EventBus) (*TUIDisplay, error) {
	m := &model{
		messages:          make([]*displayMessage, 0, maxMessages),
		autoScroll:        true,
		prettyJSON:        true,
		stats:             make(map[string]int),
		width:             80,
		height:            24,
		bannerCollapsed:   false,
		searchQuery:       "",
		searchResults:     []int{},
		currentSearchIdx:  -1,
		filterTransport:   "ALL",
		filterType:        "ALL",
		filterApp:         "ALL",
		jsonWrap:          true,
		density:           densityComfort,
		requestToResponse: make(map[string]*event.MCPEvent),
		detailViewTab:     "request",
	}

	d := &TUIDisplay{
		model:    m,
		eventBus: eventBus,
		program:  tea.NewProgram(m, tea.WithAltScreen()),
	}

	// Subscribe to MCP events
	if err := eventBus.Subscribe(event.EventTypeMCPMessage, d.handleMessage); err != nil {
		return nil, err
	}

	// Subscribe to LLM events
	if err := eventBus.Subscribe(event.EventTypeLLMMessage, d.handleLLMMessage); err != nil {
		return nil, err
	}

	return d, nil
}

// handleMessage receives MCP events and sends them to the TUI
func (d *TUIDisplay) handleMessage(e event.Event) {
	msg, ok := e.(*event.MCPEvent)
	if !ok {
		return
	}

	// Wrap in displayMessage and send to Bubbletea program
	d.program.Send(msgReceived{msg: &displayMessage{
		source:   sourceTypeMCP,
		mcpEvent: msg,
	}})
}

// handleLLMMessage receives LLM events and sends them to the TUI
func (d *TUIDisplay) handleLLMMessage(e event.Event) {
	msg, ok := e.(*event.LLMEvent)
	if !ok {
		return
	}

	// Wrap in displayMessage and send to Bubbletea program
	d.program.Send(msgReceived{msg: &displayMessage{
		source:   sourceTypeLLM,
		llmEvent: msg,
	}})
}

// Run starts the TUI
func (d *TUIDisplay) Run() error {
	// Ensure terminal is properly restored even on panic/crash
	defer func() {
		if r := recover(); r != nil {
			// Force exit alternate screen and restore terminal
			d.program.ReleaseTerminal()
			// Re-panic to preserve the original error
			panic(r)
		}
	}()

	_, err := d.program.Run()

	return err
}

// Init initializes the Bubbletea model
func (m *model) Init() tea.Cmd {
	return tea.EnterAltScreen
}

// Update handles Bubbletea messages and key presses
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case msgReceived:
		// Add message to buffer
		if !m.paused {
			m.addMessage(msg.msg)
		}
		return m, nil

	case tickMsg:
		return m, nil
	}

	return m, nil
}

// handleKeyPress handles keyboard input
func (m *model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.viewMode {
	case viewModeMain:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			// Move cursor up
			if m.selectedIndex > 0 {
				m.selectedIndex--
				m.autoScroll = false
				m.ensureVisible()
			}

		case "down", "j":
			// Move cursor down
			filteredMsgs := m.getFilteredMessages()
			if m.selectedIndex < len(filteredMsgs)-1 {
				m.selectedIndex++
				m.autoScroll = false
				m.ensureVisible()
			}

		case "enter":
			// Show detail view for selected message
			if len(m.getFilteredMessages()) > 0 {
				filteredMsgs := m.getFilteredMessages()
				selectedMsg := filteredMsgs[m.selectedIndex]

				// Set default tab based on message type (MCP only)
				if selectedMsg.source == sourceTypeMCP && selectedMsg.mcpEvent != nil {
					if selectedMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
						m.detailViewTab = "request"
					} else if selectedMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
						m.detailViewTab = "response"
					} else {
						// Notifications don't have tabs
						m.detailViewTab = "request"
					}
				} else {
					// LLM events don't have tabs (no correlation yet)
					m.detailViewTab = "request"
				}

				m.viewMode = viewModeDetail
				m.detailScroll = 0
			}

		case "p":
			m.paused = !m.paused

		case "b":
			// Toggle banner collapsed
			m.bannerCollapsed = !m.bannerCollapsed

		case "d":
			// Cycle density mode
			m.density = (m.density + 1) % 3

		case "t":
			// Cycle transport filter
			switch m.filterTransport {
			case "ALL":
				m.filterTransport = "HTTP"
			case "HTTP":
				m.filterTransport = "STDIO"
			case "STDIO":
				m.filterTransport = "ALL"
			}
			m.selectedIndex = 0
			m.scrollOffset = 0

		case "y":
			// Cycle type filter
			switch m.filterType {
			case "ALL":
				m.filterType = "REQ"
			case "REQ":
				m.filterType = "RESP"
			case "RESP":
				m.filterType = "STREAM"
			case "STREAM":
				m.filterType = "NOTIFY"
			case "NOTIFY":
				m.filterType = "ERROR"
			case "ERROR":
				m.filterType = "ALL"
			}
			m.selectedIndex = 0
			m.scrollOffset = 0

		case "a":
			// Cycle app filter
			switch m.filterApp {
			case "ALL":
				m.filterApp = "MCP"
			case "MCP":
				m.filterApp = "LLM"
			case "LLM":
				m.filterApp = "ALL"
			}
			m.selectedIndex = 0
			m.scrollOffset = 0

		case "f":
			// Toggle follow-tail mode (enable auto-scroll)
			m.autoScroll = !m.autoScroll
			if m.autoScroll {
				filteredMsgs := m.getFilteredMessages()
				if len(filteredMsgs) > 0 {
					m.selectedIndex = len(filteredMsgs) - 1
					m.ensureVisible()
				}
			}

		case "n":
			// Next search result
			if len(m.searchResults) > 0 {
				m.currentSearchIdx = (m.currentSearchIdx + 1) % len(m.searchResults)
				m.selectedIndex = m.searchResults[m.currentSearchIdx]
				m.autoScroll = false
				m.ensureVisible()
			}

		case "N":
			// Previous search result
			if len(m.searchResults) > 0 {
				m.currentSearchIdx--
				if m.currentSearchIdx < 0 {
					m.currentSearchIdx = len(m.searchResults) - 1
				}
				m.selectedIndex = m.searchResults[m.currentSearchIdx]
				m.autoScroll = false
				m.ensureVisible()
			}
		}

	case viewModeDetail:
		switch msg.String() {
		case "esc":
			m.viewMode = viewModeMain

		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.detailScroll > 0 {
				m.detailScroll--
			}

		case "down", "j":
			// Only scroll down if there's more content below
			maxScroll := m.getMaxDetailScroll()
			if m.detailScroll < maxScroll {
				m.detailScroll++
			}

		case "tab":
			m.prettyJSON = !m.prettyJSON
			m.detailScroll = 0

		case "w":
			// Toggle wrap
			m.jsonWrap = !m.jsonWrap
			m.detailScroll = 0

		case "r":
			// Switch between request and response tabs (MCP only)
			filteredMsgs := m.getFilteredMessages()
			if len(filteredMsgs) > 0 {
				selectedMsg := filteredMsgs[m.selectedIndex]
				// Only for MCP events, not LLM
				if selectedMsg.source == sourceTypeMCP && selectedMsg.mcpEvent != nil {
					// Don't switch for notifications
					if selectedMsg.mcpEvent.MessageType != event.JSONRPCMessageTypeNotification {
						if m.detailViewTab == "request" {
							// Check if response exists
							paired := m.findPairedMessage(selectedMsg)
							if paired != nil || selectedMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
								m.detailViewTab = "response"
								m.detailScroll = 0
							}
						} else {
							m.detailViewTab = "request"
							m.detailScroll = 0
						}
					}
				}
			}
		}
	}

	return m, nil
}

// getFilteredMessages returns messages that match current filters
func (m *model) getFilteredMessages() []*displayMessage {
	var filtered []*displayMessage
	for _, msg := range m.messages {
		// App filter
		if m.filterApp != "ALL" {
			if m.filterApp == "MCP" && msg.source != sourceTypeMCP {
				continue
			}
			if m.filterApp == "LLM" && msg.source != sourceTypeLLM {
				continue
			}
		}

		// Transport filter
		if m.filterTransport != "ALL" {
			transportType := m.getTransportType(msg)
			if m.filterTransport == "HTTP" && transportType != "HTTP" {
				continue
			}
			if m.filterTransport == "STDIO" && transportType != "STDIO" {
				continue
			}
		}

		// Type filter
		if m.filterType != "ALL" {
			msgType := m.getMessageTypeString(msg)
			if msgType != m.filterType {
				continue
			}
		}

		filtered = append(filtered, msg)
	}
	return filtered
}

// getTransportType returns the transport type for a displayMessage
func (m *model) getTransportType(msg *displayMessage) string {
	switch msg.source {
	case sourceTypeMCP:
		if msg.mcpEvent != nil {
			if msg.mcpEvent.TransportType == event.TransportTypeHTTP {
				return "HTTP"
			}
			return "STDIO"
		}
	case sourceTypeLLM:
		return "HTTP" // LLM events are always HTTP
	}
	return ""
}

// getMessageTypeString returns the message type as a string
func (m *model) getMessageTypeString(msg *displayMessage) string {
	switch msg.source {
	case sourceTypeMCP:
		if msg.mcpEvent == nil {
			return "UNKNOWN"
		}
		switch msg.mcpEvent.MessageType {
		case event.JSONRPCMessageTypeRequest:
			return "REQ"
		case event.JSONRPCMessageTypeResponse:
			if msg.mcpEvent.Error.Message != "" {
				return "ERROR"
			}
			return "RESP"
		case event.JSONRPCMessageTypeNotification:
			return "NOTIFY"
		default:
			return "UNKNOWN"
		}
	case sourceTypeLLM:
		if msg.llmEvent == nil {
			return "UNKNOWN"
		}
		switch msg.llmEvent.MessageType {
		case event.LLMMessageTypeRequest:
			return "REQ"
		case event.LLMMessageTypeResponse:
			if msg.llmEvent.Error != "" {
				return "ERROR"
			}
			return "RESP"
		case event.LLMMessageTypeStreamChunk:
			return "STREAM"
		default:
			return "UNKNOWN"
		}
	}
	return "UNKNOWN"
}

// addMessage adds a message to the circular buffer
func (m *model) addMessage(msg *displayMessage) {
	m.messages = append(m.messages, msg)

	// Update request-response mapping for MCP responses only
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
			pairingKey := getPairingKey(msg.mcpEvent)
			if pairingKey != "" {
				m.requestToResponse[pairingKey] = msg.mcpEvent
			}
		}

		// Update statistics for MCP messages
		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest || msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			m.stats[msg.mcpEvent.Method]++
		}
	}

	// Circular buffer: remove oldest if over limit
	if len(m.messages) > maxMessages {
		oldestMsg := m.messages[0]
		// Clean up request-response mapping if removing an MCP response
		if oldestMsg.source == sourceTypeMCP && oldestMsg.mcpEvent != nil {
			if oldestMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse && oldestMsg.mcpEvent.ID != nil {
				pairingKey := getPairingKey(oldestMsg.mcpEvent)
				if pairingKey != "" {
					delete(m.requestToResponse, pairingKey)
				}
			}
		}

		m.messages = m.messages[1:]
		if m.scrollOffset > 0 {
			m.scrollOffset--
		}
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
	}

	// Auto-scroll: move cursor and viewport to latest
	if m.autoScroll {
		filteredMsgs := m.getFilteredMessages()
		if len(filteredMsgs) > 0 {
			m.selectedIndex = len(filteredMsgs) - 1
			visibleLines := m.getVisibleLines()
			m.scrollOffset = max(0, len(filteredMsgs)-visibleLines)
		}
	}
}

// ensureVisible ensures the selected item is visible in the viewport
func (m *model) ensureVisible() {
	visibleLines := m.getVisibleLines()
	if m.selectedIndex < m.scrollOffset {
		m.scrollOffset = m.selectedIndex
	} else if m.selectedIndex >= m.scrollOffset+visibleLines {
		m.scrollOffset = m.selectedIndex - visibleLines + 1
	}
}

// getVisibleLines calculates how many message lines can fit in the viewport
func (m *model) getVisibleLines() int {
	// Calculate header size based on banner state and terminal width
	headerLines := 0
	if m.bannerCollapsed {
		headerLines = 1 // Just one line with title and status
	} else if m.width < 100 {
		headerLines = 2 // Compact header (title + status)
	} else {
		headerLines = 6 // Full ASCII art
	}

	// Count all UI elements based on density mode
	usedLines := headerLines

	switch m.density {
	case densityUltra:
		// Ultra: minimal spacing and borders
		usedLines += 4 // separator(1) + table header(1) + footer(2)
	case densityCompact:
		// Compact: reduced spacing
		usedLines += 6 // blank(1) + separator(1) + table header(1) + separator(1) + footer(2)
	case densityComfort:
		// Comfort: original spacing
		usedLines += 10 // blank(2) + separator(1) + table header(1) + separator(1) + stats(1) + separator(1) + footer(3)
	}

	return max(1, m.height-usedLines)
}

// View renders the TUI
func (m *model) View() string {
	switch m.viewMode {
	case viewModeMain:
		return m.renderMainView()
	case viewModeDetail:
		return m.renderDetailView()
	}
	return ""
}

// renderMainView renders the main table view
func (m *model) renderMainView() string {
	var b strings.Builder
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))

	// Header
	b.WriteString(m.renderHeader())

	// Spacing and separators based on density
	switch m.density {
	case densityUltra:
		b.WriteString("\n")
		b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
	case densityCompact:
		b.WriteString("\n")
		b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
	case densityComfort:
		b.WriteString("\n\n")
		b.WriteString(sepStyle.Render(strings.Repeat("━", m.width)))
	}
	b.WriteString("\n")

	// Column headers
	b.WriteString(m.renderTableHeader())
	b.WriteString("\n")

	// Messages table
	b.WriteString(m.renderMessages())

	// Statistics and footer based on density
	switch m.density {
	case densityUltra:
		// Ultra: no stats, minimal footer
		b.WriteString("\n")
		b.WriteString(m.renderFooter())
	case densityCompact:
		// Compact: separator + footer
		b.WriteString("\n")
		b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
		b.WriteString("\n")
		b.WriteString(m.renderFooter())
	case densityComfort:
		// Comfort: separator + stats + separator + footer
		b.WriteString("\n")
		b.WriteString(sepStyle.Render(strings.Repeat("━", m.width)))
		b.WriteString("\n")
		b.WriteString(m.renderStats())
		b.WriteString("\n")
		b.WriteString(sepStyle.Render(strings.Repeat("━", m.width)))
		b.WriteString("\n")
		b.WriteString(m.renderFooter())
	}

	return b.String()
}

// renderHeader renders the ASCII art header
func (m *model) renderHeader() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)

	status := "MONITORING"
	if m.paused {
		status = "PAUSED"
	}
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	if m.paused {
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	}

	timeStr := time.Now().Format("15:04:05")

	// Collapsed banner mode - single line
	if m.bannerCollapsed {
		versionStr := ""
		if len(version.Version) > 0 {
			versionStr = " " + version.Version
		}
		// Build filter indicators
		var filters []string
		if m.filterTransport != "ALL" {
			filters = append(filters, fmt.Sprintf("T:%s", m.filterTransport))
		}
		if m.filterApp != "ALL" {
			filters = append(filters, fmt.Sprintf("A:%s", m.filterApp))
		}
		if m.filterType != "ALL" {
			filters = append(filters, fmt.Sprintf("Y:%s", m.filterType))
		}
		filterStr := ""
		if len(filters) > 0 {
			filterStr = " [" + strings.Join(filters, " ") + "]"
		}

		title := fmt.Sprintf("MCPSpy%s %s %s%s", versionStr, statusStyle.Render(status), timeStr, filterStr)
		b.WriteString(headerStyle.Render(title))
		return b.String()
	}

	// Use compact header for narrow terminals
	if m.width < 100 {
		versionStr := ""
		if len(version.Version) > 0 {
			versionStr = " " + version.Version
		}
		title := fmt.Sprintf("MCPSpy%s - MCP Monitor", versionStr)
		b.WriteString(headerStyle.Render(title))
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(fmt.Sprintf("[●] %s  %s", statusStyle.Render(status), timeStr)))
		return b.String()
	}

	// ASCII art width is 54 characters
	asciiWidth := 54

	// Line 1: ASCII art only
	b.WriteString(headerStyle.Render("███╗   ███╗ ██████╗██████╗ ███████╗██████╗ ██╗   ██╗"))
	b.WriteString("\n")

	// Line 2: ASCII art only
	b.WriteString(headerStyle.Render("████╗ ████║██╔════╝██╔══██╗██╔════╝██╔══██╗╚██╗ ██╔╝"))
	b.WriteString("\n")

	// Line 3: ASCII art + title/version
	line3Ascii := "██╔████╔██║██║     ██████╔╝███████╗██████╔╝ ╚████╔╝ "
	versionStr := ""
	if len(version.Version) > 0 {
		versionStr = version.Version
	}
	title := fmt.Sprintf("   MCPSpy %s  -  Model Context Protocol Monitor", versionStr)
	if len(line3Ascii)+len(title) > m.width {
		title = fmt.Sprintf("   MCPSpy %s  -  MCP Monitor", versionStr)
	}
	line3 := headerStyle.Render(line3Ascii + title)
	b.WriteString(line3)
	b.WriteString("\n")

	// Line 4: ASCII art only
	b.WriteString(headerStyle.Render("██║╚██╔╝██║██║     ██╔═══╝ ╚════██║██╔═══╝   ╚██╔╝  "))
	b.WriteString("\n")

	// Line 5: ASCII art + status + time
	line5Ascii := "██║ ╚═╝ ██║╚██████╗██║     ███████║██║        ██║   "
	statusText := fmt.Sprintf("   [●] %s", statusStyle.Render(status))
	// Calculate padding to right-align time
	availableSpace := m.width - asciiWidth - lipgloss.Width(statusText) - len(timeStr) - 2
	padding := max(0, availableSpace)
	line5 := line5Ascii + statusText + strings.Repeat(" ", padding) + timeStr
	b.WriteString(headerStyle.Render(line5))
	b.WriteString("\n")

	// Line 6: ASCII art only
	b.WriteString(headerStyle.Render("╚═╝     ╚═╝ ╚═════╝╚═╝     ╚══════╝╚═╝        ╚═╝   "))

	return b.String()
}

// getColumnWidths calculates responsive column widths based on window width
func (m *model) getColumnWidths() (time, transport, app, msgType, id, from, to, method int) {
	// Minimum widths
	minTime := 12
	minTransport := 9
	minApp := 3
	minType := 6
	minID := 4
	minFrom := 15
	minTo := 15
	minMethod := 20

	// Fixed elements: prefix(2) + arrow(3) + spaces(9) = 14 (one more space for app column)
	fixedWidth := 14

	availableWidth := m.width - fixedWidth
	if availableWidth < 80 {
		// Very narrow terminal - use minimum widths
		return minTime, minTransport, minApp, minType, minID, minFrom, minTo, minMethod
	}

	// Allocate widths proportionally
	totalMin := minTime + minTransport + minApp + minType + minID + minFrom + minTo + minMethod
	extraSpace := availableWidth - totalMin

	if extraSpace < 0 {
		// Terminal too narrow, use minimums
		return minTime, minTransport, minApp, minType, minID, minFrom, minTo, minMethod
	}

	// Distribute extra space (prioritize method column)
	methodExtra := (extraSpace * 40) / 100
	fromExtra := (extraSpace * 20) / 100
	toExtra := (extraSpace * 20) / 100
	remaining := extraSpace - methodExtra - fromExtra - toExtra

	return minTime, minTransport + remaining/4, minApp, minType + remaining/4, minID,
		minFrom + fromExtra, minTo + toExtra, minMethod + methodExtra
}

// padString pads a string to the specified width (left-aligned) and returns it
func padString(s string, width int) string {
	visibleLen := len(s)
	if visibleLen >= width {
		if visibleLen > width {
			// Truncate
			return s[:max(0, width-3)] + "..."
		}
		return s
	}
	return s + strings.Repeat(" ", width-visibleLen)
}

// padStringRight pads a string to the specified width (right-aligned) and returns it
func padStringRight(s string, width int) string {
	visibleLen := len(s)
	if visibleLen >= width {
		if visibleLen > width {
			// Truncate
			return s[:max(0, width-3)] + "..."
		}
		return s
	}
	return strings.Repeat(" ", width-visibleLen) + s
}

// renderTableHeader renders the column headers
func (m *model) renderTableHeader() string {
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9E9E9E")).
		Bold(true)

	timeW, transportW, appW, typeW, idW, fromW, toW, _ := m.getColumnWidths()

	// Pad each field, then concatenate
	var b strings.Builder
	b.WriteString("  ") // Prefix (2) to match message rows
	b.WriteString(padStringRight("TIME", timeW))
	b.WriteString(" ")
	b.WriteString(padString("TRANSPORT", transportW))
	b.WriteString(" ")
	b.WriteString(padString("APP", appW))
	b.WriteString(" ")
	b.WriteString(padString("TYPE", typeW))
	b.WriteString(" ")
	b.WriteString(padString("ID", idW))
	b.WriteString(" ")
	b.WriteString(padString("FROM", fromW))
	b.WriteString(" ")
	b.WriteString("→")
	b.WriteString(" ")
	b.WriteString(padString("TO", toW))
	b.WriteString(" ")
	b.WriteString("DETAILS") // No padding - extends to edge

	return headerStyle.Render(b.String())
}

// renderMessages renders the message table
func (m *model) renderMessages() string {
	var b strings.Builder

	filteredMsgs := m.getFilteredMessages()
	visibleLines := m.getVisibleLines()
	start := m.scrollOffset
	end := min(start+visibleLines, len(filteredMsgs))

	for i := start; i < end; i++ {
		msg := filteredMsgs[i]
		line := m.renderMessageLine(msg, i == m.selectedIndex)
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// renderMessageLine renders a single message line
func (m *model) renderMessageLine(msg *displayMessage, selected bool) string {
	// Check if this message's pair is selected (for subtle highlighting) - only for MCP
	isPairHighlighted := false
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		isPairHighlighted = m.isPairHighlighted(msg)
	}

	// Get dynamic column widths
	timeW, transportW, appW, typeW, idW, fromW, toW, _ := m.getColumnWidths()

	// Get timestamp
	var timestamp time.Time
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		timestamp = msg.mcpEvent.Timestamp
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		timestamp = msg.llmEvent.Timestamp
	}

	// Colorblind-safe palette
	// Time - same as transport for better visibility on gray background
	timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
	timeStr := padStringRight(timestamp.Format("15:04:05.000"), timeW)

	// Transport - neutral gray
	transportStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
	transportType := m.getTransportType(msg)
	transportStr := padString(transportType, transportW)

	// App - color coded with unique colors
	var appStyle lipgloss.Style
	var appStr string
	if msg.source == sourceTypeMCP {
		appStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB")) // Sky Blue (unique)
		appStr = padString("MCP", appW)
	} else {
		appStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#DA70D6")) // Orchid/Pink (unique)
		appStr = padString("LLM", appW)
	}

	// Type - colorblind-safe palette
	msgTypeStr := m.getMessageTypeString(msg)
	var typeStyle lipgloss.Style
	var typeStr string
	var isError bool
	switch msgTypeStr {
	case "REQ":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5F87FF")) // Blue
		typeStr = padString("REQ", typeW)
	case "RESP":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787")) // Green
		typeStr = padString("RESP", typeW)
	case "ERROR":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Bold(true) // Red
		typeStr = padString("ERROR", typeW)
		isError = true
	case "NOTIFY":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8700")) // Orange
		typeStr = padString("NOTIFY", typeW)
	case "STREAM":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#AF87FF")) // Purple
		typeStr = padString("STREAM", typeW)
	default:
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
		typeStr = padString(msgTypeStr, typeW)
	}

	// ID - same as transport for better visibility on gray background
	idStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
	var idStr string
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		idStr = fmt.Sprintf("%v", msg.mcpEvent.ID)
		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			idStr = "-"
		}
	} else {
		idStr = "-" // LLM messages have no ID
	}
	idStr = padString(idStr, idW)

	// From/To - follows console pattern
	processNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))
	var fromStr, toStr string

	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		switch msg.mcpEvent.TransportType {
		case event.TransportTypeStdio:
			fromPlain := fmt.Sprintf("%s[%d]", msg.mcpEvent.FromComm, msg.mcpEvent.FromPID)
			toPlain := fmt.Sprintf("%s[%d]", msg.mcpEvent.ToComm, msg.mcpEvent.ToPID)
			fromStr = padString(fromPlain, fromW)
			toStr = padString(toPlain, toW)
		case event.TransportTypeHTTP:
			if msg.mcpEvent.HttpTransport.IsRequest {
				fromPlain := fmt.Sprintf("%s[%d]", msg.mcpEvent.HttpTransport.Comm, msg.mcpEvent.HttpTransport.PID)
				toPlain := msg.mcpEvent.HttpTransport.Host
				fromStr = padString(fromPlain, fromW)
				toStr = padString(toPlain, toW)
			} else {
				fromPlain := msg.mcpEvent.HttpTransport.Host
				toPlain := fmt.Sprintf("%s[%d]", msg.mcpEvent.HttpTransport.Comm, msg.mcpEvent.HttpTransport.PID)
				fromStr = padString(fromPlain, fromW)
				toStr = padString(toPlain, toW)
			}
		}
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		// LLM: request goes PID→host, response/stream goes host→PID
		if msg.llmEvent.MessageType == event.LLMMessageTypeRequest {
			fromPlain := fmt.Sprintf("%s[%d]", msg.llmEvent.Comm, msg.llmEvent.PID)
			toPlain := msg.llmEvent.Host
			fromStr = padString(fromPlain, fromW)
			toStr = padString(toPlain, toW)
		} else {
			fromPlain := msg.llmEvent.Host
			toPlain := fmt.Sprintf("%s[%d]", msg.llmEvent.Comm, msg.llmEvent.PID)
			fromStr = padString(fromPlain, fromW)
			toStr = padString(toPlain, toW)
		}
	}

	// Method/Details - no fixed padding, let it extend to terminal edge
	methodStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	var detailsStr string

	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		switch msg.mcpEvent.MessageType {
		case event.JSONRPCMessageTypeRequest:
			detailsStr = msg.mcpEvent.Method
			if toolName := msg.mcpEvent.ExtractToolName(); toolName != "" {
				detailsStr += fmt.Sprintf(" (%s)", toolName)
			} else if uri := msg.mcpEvent.ExtractResourceURI(); uri != "" {
				detailsStr += fmt.Sprintf(" (%s)", uri)
			}
		case event.JSONRPCMessageTypeResponse:
			if msg.mcpEvent.Error.Message != "" {
				detailsStr = fmt.Sprintf("%s (Code: %d)", msg.mcpEvent.Error.Message, msg.mcpEvent.Error.Code)
			} else {
				detailsStr = "OK"
			}
		case event.JSONRPCMessageTypeNotification:
			detailsStr = msg.mcpEvent.Method
		}
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		// LLM: [model] "content" - build complete string first, then trim to fit
		model := msg.llmEvent.Model
		content := strings.ReplaceAll(msg.llmEvent.Content, "\n", " ") // Replace newlines with spaces

		// Build the full details string
		if model != "" && content != "" {
			detailsStr = fmt.Sprintf("[%s] \"%s\"", model, content)
		} else if model != "" {
			detailsStr = fmt.Sprintf("[%s]", model)
		} else if content != "" {
			detailsStr = fmt.Sprintf("\"%s\"", content)
		}
		// Trimming will happen below based on available width
	}

	// Calculate remaining space for details (to prevent overflow)
	// prefix(2) + timeW + transport + app + typeW + idW + fromW + arrow + toW + spaces(8)
	// Note: arrow "→" is 3 bytes in UTF-8 but displays as 1 character width
	arrowWidth := 1 // Display width of arrow
	usedWidth := 2 + timeW + 1 + transportW + 1 + appW + 1 + typeW + 1 + idW + 1 + fromW + 1 + arrowWidth + 1 + toW + 1
	remainingWidth := m.width - usedWidth
	if len(detailsStr) > remainingWidth && remainingWidth > 3 {
		detailsStr = detailsStr[:remainingWidth-3] + "..."
	}
	// Ensure detailsStr is padded to remainingWidth for consistent row width
	if len(detailsStr) < remainingWidth {
		detailsStr = padString(detailsStr, remainingWidth)
	}

	// Selection marker or error gutter
	prefix := "  "
	if isError && !selected {
		prefix = "! " // Red gutter for errors
	} else if selected {
		prefix = "▶ "
	}

	// Construct line
	var b strings.Builder

	if selected {
		// For selected rows, build plain text line and apply highlight to entire row
		b.WriteString(prefix)
		b.WriteString(timeStr)
		b.WriteString(" ")
		b.WriteString(transportStr)
		b.WriteString(" ")
		b.WriteString(appStr)
		b.WriteString(" ")
		b.WriteString(typeStr)
		b.WriteString(" ")
		b.WriteString(idStr)
		b.WriteString(" ")
		b.WriteString(fromStr)
		b.WriteString(" ")
		b.WriteString("→")
		b.WriteString(" ")
		b.WriteString(toStr)
		b.WriteString(" ")
		b.WriteString(detailsStr)

		// Pad to full terminal width to extend highlight to the right edge
		currentLen := len(prefix) + len(timeStr) + 1 + len(transportStr) + 1 + len(appStr) + 1 + len(typeStr) + 1 +
			len(idStr) + 1 + len(fromStr) + 1 + 1 + 1 + len(toStr) + 1 + len(detailsStr)
		if currentLen < m.width {
			b.WriteString(strings.Repeat(" ", m.width-currentLen))
		}

		// Apply highlight background
		selectedStyle := lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3A3A")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true)
		return selectedStyle.Render(b.String())
	}

	// For pair-highlighted rows, apply individual color styles with background
	if isPairHighlighted {
		bgColor := lipgloss.Color("#505050") // Light gray, lighter than main cursor
		gutterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")).Background(bgColor)
		arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080")).Background(bgColor)

		// Apply background to all styles
		timeStyleBg := timeStyle.Background(bgColor)
		transportStyleBg := transportStyle.Background(bgColor)
		appStyleBg := appStyle.Background(bgColor)
		typeStyleBg := typeStyle.Background(bgColor)
		idStyleBg := idStyle.Background(bgColor)
		processNameStyleBg := processNameStyle.Background(bgColor)
		methodStyleBg := methodStyle.Background(bgColor)

		if isError {
			b.WriteString(gutterStyle.Render(prefix))
		} else {
			b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(prefix))
		}
		b.WriteString(timeStyleBg.Render(timeStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(transportStyleBg.Render(transportStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(appStyleBg.Render(appStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(typeStyleBg.Render(typeStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(idStyleBg.Render(idStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(processNameStyleBg.Render(fromStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(arrowStyle.Render("→"))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(processNameStyleBg.Render(toStr))
		b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(" "))
		b.WriteString(methodStyleBg.Render(detailsStr))

		// Pad to full width with background
		currentLen := len(prefix) + len(timeStr) + 1 + len(transportStr) + 1 + len(appStr) + 1 + len(typeStr) + 1 +
			len(idStr) + 1 + len(fromStr) + 1 + 1 + 1 + len(toStr) + 1 + len(detailsStr)
		if currentLen < m.width {
			b.WriteString(lipgloss.NewStyle().Background(bgColor).Render(strings.Repeat(" ", m.width-currentLen)))
		}

		return b.String()
	}

	// For non-selected rows, apply individual color styles
	gutterStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))

	if isError {
		b.WriteString(gutterStyle.Render(prefix))
	} else {
		b.WriteString(prefix)
	}
	b.WriteString(timeStyle.Render(timeStr))
	b.WriteString(" ")
	b.WriteString(transportStyle.Render(transportStr))
	b.WriteString(" ")
	b.WriteString(appStyle.Render(appStr))
	b.WriteString(" ")
	b.WriteString(typeStyle.Render(typeStr))
	b.WriteString(" ")
	b.WriteString(idStyle.Render(idStr))
	b.WriteString(" ")
	b.WriteString(processNameStyle.Render(fromStr))
	b.WriteString(" ")
	b.WriteString(arrowStyle.Render("→"))
	b.WriteString(" ")
	b.WriteString(processNameStyle.Render(toStr))
	b.WriteString(" ")
	b.WriteString(methodStyle.Render(detailsStr))

	return b.String()
}

// renderStats renders the statistics footer
func (m *model) renderStats() string {
	statsLabelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	statsNumStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	statsMethodStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))

	// Sort methods by count
	type methodCount struct {
		method string
		count  int
	}
	var methods []methodCount
	for method, count := range m.stats {
		methods = append(methods, methodCount{method, count})
	}
	sort.Slice(methods, func(i, j int) bool {
		// Sort by count descending, then by method name ascending for stable ordering
		if methods[i].count != methods[j].count {
			return methods[i].count > methods[j].count
		}
		return methods[i].method < methods[j].method
	})

	// Build stats string
	var parts []string
	parts = append(parts, fmt.Sprintf("%s %s msgs",
		statsLabelStyle.Render("Stats:"),
		statsNumStyle.Render(fmt.Sprintf("%d", len(m.messages)))))

	// Add top 3 methods
	for i := 0; i < min(3, len(methods)); i++ {
		parts = append(parts, fmt.Sprintf("%s %s",
			statsNumStyle.Render(fmt.Sprintf("%d", methods[i].count)),
			statsMethodStyle.Render(methods[i].method)))
	}

	return "  " + strings.Join(parts, " │ ")
}

// renderFooter renders the keyboard shortcuts footer
func (m *model) renderFooter() string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF"))
	infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))

	// First line: keyboard shortcuts
	var shortcuts []string
	switch m.density {
	case densityUltra:
		// Ultra compact: minimal shortcuts
		shortcuts = []string{
			keyStyle.Render("↑↓"),
			keyStyle.Render("Enter"),
			keyStyle.Render("p"),
			keyStyle.Render("t"),
			keyStyle.Render("a"),
			keyStyle.Render("y"),
			keyStyle.Render("d"),
			keyStyle.Render("b"),
			keyStyle.Render("q"),
		}
	case densityCompact:
		// Compact: short labels
		shortcuts = []string{
			keyStyle.Render("↑↓:Nav"),
			keyStyle.Render("Enter:Detail"),
			keyStyle.Render("p:Pause"),
			keyStyle.Render("t:Transport"),
			keyStyle.Render("a:App"),
			keyStyle.Render("y:Type"),
			keyStyle.Render("d:Density"),
			keyStyle.Render("b:Banner"),
			keyStyle.Render("f:Follow"),
			keyStyle.Render("q:Quit"),
		}
	case densityComfort:
		// Comfort: full labels
		shortcuts = []string{
			keyStyle.Render("↑↓:Navigate"),
			keyStyle.Render("Enter:Details"),
			keyStyle.Render("p:Pause"),
			keyStyle.Render("t:TransportFilter"),
			keyStyle.Render("a:AppFilter"),
			keyStyle.Render("y:TypeFilter"),
			keyStyle.Render("d:Density"),
			keyStyle.Render("b:Banner"),
			keyStyle.Render("f:FollowTail"),
			keyStyle.Render("q:Quit"),
		}
	}

	line1 := "  " + strings.Join(shortcuts, " │ ")

	// Second line: buffer info and filters (only in comfort mode)
	if m.density == densityComfort {
		filteredCount := len(m.getFilteredMessages())
		totalCount := len(m.messages)
		bufferInfo := fmt.Sprintf("Buffer: %d/%d msgs", filteredCount, totalCount)

		// Active filters
		var filterInfo []string
		if m.filterTransport != "ALL" {
			filterInfo = append(filterInfo, fmt.Sprintf("Transport=%s", m.filterTransport))
		}
		if m.filterApp != "ALL" {
			filterInfo = append(filterInfo, fmt.Sprintf("App=%s", m.filterApp))
		}
		if m.filterType != "ALL" {
			filterInfo = append(filterInfo, fmt.Sprintf("Type=%s", m.filterType))
		}
		if !m.autoScroll {
			filterInfo = append(filterInfo, "SCROLLING")
		}

		line2Parts := []string{bufferInfo}
		if len(filterInfo) > 0 {
			line2Parts = append(line2Parts, "Filters: "+strings.Join(filterInfo, ", "))
		}
		line2 := "  " + infoStyle.Render(strings.Join(line2Parts, " │ "))

		return line1 + "\n" + line2
	}

	return line1
}

// renderTabBar renders the tab navigation bar for request/response switching
func (m *model) renderTabBar(msg *displayMessage) string {
	// Only for MCP events
	if msg.source != sourceTypeMCP || msg.mcpEvent == nil {
		return ""
	}

	// Don't show tabs for notifications
	if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
		return ""
	}

	var b strings.Builder

	// Styles - no padding to avoid height glitches
	activeTabStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#4A4A4A")).
		Foreground(lipgloss.Color("#FFFFFF"))

	inactiveTabStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#2E2E2E")).
		Foreground(lipgloss.Color("#9E9E9E"))

	disabledTabStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1E1E1E")).
		Foreground(lipgloss.Color("#6C6C6C"))

	// Determine which tabs are available
	hasResponse := false
	responseLabel := "Response"

	paired := m.findPairedMessage(msg)
	if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
		hasResponse = true
		if msg.mcpEvent.Error.Message != "" {
			responseLabel = "Response (error)"
		}
	} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
		if paired != nil {
			hasResponse = true
			// Check if response is an error
			if paired.Error.Message != "" {
				responseLabel = "Response (error)"
			}
		} else {
			responseLabel = "Response (pending)"
		}
	}

	// Render request tab (with manual padding)
	if m.detailViewTab == "request" {
		b.WriteString(activeTabStyle.Render(" Request "))
	} else {
		b.WriteString(inactiveTabStyle.Render(" Request "))
	}

	b.WriteString(" ")

	// Render response tab (with manual padding)
	if !hasResponse {
		b.WriteString(disabledTabStyle.Render(" " + responseLabel + " "))
	} else if m.detailViewTab == "response" {
		b.WriteString(activeTabStyle.Render(" " + responseLabel + " "))
	} else {
		b.WriteString(inactiveTabStyle.Render(" " + responseLabel + " "))
	}

	return b.String()
}

// renderDetailView renders the detail view for a selected message
func (m *model) renderDetailView() string {
	filteredMsgs := m.getFilteredMessages()
	if len(filteredMsgs) == 0 {
		return "No message selected"
	}

	msg := filteredMsgs[m.selectedIndex]

	var b strings.Builder

	// Header
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	escStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF"))

	header := fmt.Sprintf("%s%s%s",
		titleStyle.Render("MESSAGE DETAILS"),
		strings.Repeat(" ", max(0, m.width-28)),
		escStyle.Render("[Esc] Back"))
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E")).Render(strings.Repeat("━", m.width)))
	b.WriteString("\n")

	// Tab bar (only for MCP events, not LLM)
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		tabBar := m.renderTabBar(msg)
		if tabBar != "" {
			b.WriteString(tabBar)
			b.WriteString("\n")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#4E4E4E")).Render(strings.Repeat("─", m.width)))
			b.WriteString("\n")
		}
	}

	// Overview section
	b.WriteString(m.renderOverview(msg))
	b.WriteString("\n")

	// Raw JSON section
	b.WriteString(m.renderRawJSON(msg))

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E")).Render(strings.Repeat("━", m.width)))
	b.WriteString("\n")

	// Footer
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF"))

	// Show 'r' keybind only for MCP messages with pairs (not notifications or LLM)
	var footer string
	showSwitch := false
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		if msg.mcpEvent.MessageType != event.JSONRPCMessageTypeNotification {
			showSwitch = true
		}
	}

	if showSwitch {
		footer = fmt.Sprintf("%s  %s  %s  %s  %s",
			keyStyle.Render("Tab:Format"),
			keyStyle.Render("r:Switch"),
			keyStyle.Render("w:Wrap"),
			keyStyle.Render("↑↓:Scroll"),
			keyStyle.Render("Esc:Back"))
	} else {
		footer = fmt.Sprintf("%s  %s  %s  %s",
			keyStyle.Render("Tab:Format"),
			keyStyle.Render("w:Wrap"),
			keyStyle.Render("↑↓:Scroll"),
			keyStyle.Render("Esc:Back"))
	}
	b.WriteString(footer)

	return b.String()
}

// renderOverview renders the overview section in detail view
func (m *model) renderOverview(msg *displayMessage) string {
	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4E4E4E"))

	// Simple header with separator
	b.WriteString(labelStyle.Bold(true).Render("OVERVIEW"))
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Helper function to render a field line (no borders, just indented)
	renderField := func(label, value string) {
		b.WriteString("  ")
		b.WriteString(labelStyle.Render(label))
		b.WriteString(valueStyle.Render(value))
		b.WriteString("\n")
	}

	// Handle MCP events
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		// Determine which MCP message to show based on active tab
		var displayMsg *event.MCPEvent

		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			displayMsg = msg.mcpEvent
		} else {
			// For request-response pairs, show content based on active tab
			if m.detailViewTab == "request" {
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					// Show the paired request
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else if msg.mcpEvent.Request != nil {
						// Create a temporary message from embedded request for display
						displayMsg = &event.MCPEvent{
							Timestamp:      msg.mcpEvent.Timestamp,
							TransportType:  msg.mcpEvent.TransportType,
							StdioTransport: msg.mcpEvent.StdioTransport,
							HttpTransport:  msg.mcpEvent.HttpTransport,
							JSONRPCMessage: *msg.mcpEvent.Request,
							Raw:            "",
						}
					} else {
						displayMsg = msg.mcpEvent // Fallback
					}
				}
			} else {
				// Show response
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					// Show the paired response
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else {
						displayMsg = nil // No response yet
					}
				}
			}
		}

		// If no message to display (e.g., pending response)
		if displayMsg == nil {
			renderField("Status:        ", "(Response pending)")
			return b.String()
		}

		// MCP event fields
		renderField("Timestamp:     ", displayMsg.Timestamp.Format("2006-01-02 15:04:05.000"))
		renderField("Transport:     ", string(displayMsg.TransportType))
		renderField("App:           ", "MCP")
		renderField("Message Type:  ", string(displayMsg.MessageType))

		idStr := fmt.Sprintf("%v", displayMsg.ID)
		if displayMsg.MessageType == event.JSONRPCMessageTypeNotification {
			idStr = "-"
		}
		renderField("Message ID:    ", idStr)

		if displayMsg.MessageType == event.JSONRPCMessageTypeResponse {
			status := "OK"
			if displayMsg.Error.Message != "" {
				status = fmt.Sprintf("Error: %s", displayMsg.Error.Message)
			}
			renderField("MCP Status:    ", status)
		}

		// From/To Process
		switch displayMsg.TransportType {
		case event.TransportTypeStdio:
			fromStr := fmt.Sprintf("%s (PID: %d)", displayMsg.FromComm, displayMsg.FromPID)
			renderField("From Process:  ", fromStr)

			toStr := fmt.Sprintf("%s (PID: %d)", displayMsg.ToComm, displayMsg.ToPID)
			renderField("To Process:    ", toStr)
		case event.TransportTypeHTTP:
			if displayMsg.HttpTransport.IsRequest {
				fromStr := fmt.Sprintf("%s (PID: %d)", displayMsg.HttpTransport.Comm, displayMsg.HttpTransport.PID)
				renderField("From Process:  ", fromStr)
				renderField("To Host:       ", displayMsg.HttpTransport.Host)
			} else {
				renderField("From Host:     ", displayMsg.HttpTransport.Host)
				toStr := fmt.Sprintf("%s (PID: %d)", displayMsg.HttpTransport.Comm, displayMsg.HttpTransport.PID)
				renderField("To Process:    ", toStr)
			}
		}
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		// Handle LLM events (aligned with HTTP MCP structure)
		renderField("Timestamp:     ", msg.llmEvent.Timestamp.Format("2006-01-02 15:04:05.000"))
		renderField("Transport:     ", "HTTP")
		renderField("App:           ", "LLM")
		renderField("Message Type:  ", string(msg.llmEvent.MessageType))
		renderField("Message ID:    ", "-")

		if msg.llmEvent.Model != "" {
			renderField("Model:         ", msg.llmEvent.Model)
		}

		// Error field - only show if there's an error
		if msg.llmEvent.Error != "" {
			renderField("Error:         ", msg.llmEvent.Error)
		}

		// From/To - aligned with HTTP MCP pattern
		if msg.llmEvent.MessageType == event.LLMMessageTypeRequest {
			// Request: From Process → To Host
			fromStr := fmt.Sprintf("%s (PID: %d)", msg.llmEvent.Comm, msg.llmEvent.PID)
			renderField("From Process:  ", fromStr)
			renderField("To Host:       ", msg.llmEvent.Host)
		} else {
			// Response/Stream: From Host → To Process
			renderField("From Host:     ", msg.llmEvent.Host)
			toStr := fmt.Sprintf("%s (PID: %d)", msg.llmEvent.Comm, msg.llmEvent.PID)
			renderField("To Process:    ", toStr)
		}
	}

	return b.String()
}

// renderRawJSON renders the raw JSON section
func (m *model) renderRawJSON(msg *displayMessage) string {
	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4E4E4E"))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF"))

	var jsonStr string
	var contentLabel string

	// Handle LLM events (simpler - no tabs, use raw JSON from HTTP payload)
	if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		contentLabel = "RAW JSON"

		// Use the original HTTP payload JSON
		rawJSON := msg.llmEvent.RawJSON
		if rawJSON == "" {
			// Fallback for stream chunks which don't have raw JSON
			rawJSON = m.reconstructLLMJSON(msg.llmEvent)
		}

		// Apply formatting based on prettyJSON toggle
		var jsonObj interface{}
		if err := json.Unmarshal([]byte(rawJSON), &jsonObj); err == nil {
			if m.prettyJSON {
				// Pretty print with indentation
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = rawJSON
				}
			} else {
				// Compact JSON (single line)
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = rawJSON
				}
			}
		} else {
			jsonStr = rawJSON
		}
	} else if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		// MCP event - original logic with tab support
		// Determine which message to show based on active tab
		var displayMsg *event.MCPEvent

		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			// Notifications don't have tabs
			displayMsg = msg.mcpEvent
			contentLabel = "RAW JSON"
		} else {
			// For request-response pairs, show content based on active tab
			if m.detailViewTab == "request" {
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					// Show the paired request
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else if msg.mcpEvent.Request != nil {
						// Create a temporary message from embedded request for display
						displayMsg = &event.MCPEvent{
							JSONRPCMessage: *msg.mcpEvent.Request,
							Raw:            "", // We'll generate this
						}
						// Generate raw JSON for embedded request
						if rawBytes, err := json.Marshal(msg.mcpEvent.Request); err == nil {
							displayMsg.Raw = string(rawBytes)
						}
					} else {
						displayMsg = msg.mcpEvent // Fallback
					}
				}
				contentLabel = "REQUEST JSON"
			} else {
				// Show response
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					// Show the paired response
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else {
						displayMsg = nil // No response yet
					}
				}
				contentLabel = "RESPONSE JSON"
			}
		}

		// If no message to display (e.g., pending response)
		if displayMsg == nil {
			// Simple header with hints
			wrapStatus := "OFF"
			if m.jsonWrap {
				wrapStatus = "ON"
			}
			headerLabel := labelStyle.Bold(true).Render(contentLabel)
			header := fmt.Sprintf("%s  %s  %s",
				headerLabel,
				hintStyle.Render("[Tab:Format]"),
				hintStyle.Render(fmt.Sprintf("[w:Wrap=%s]", wrapStatus)))
			b.WriteString(header)
			b.WriteString("\n")
			b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
			b.WriteString("\n")
			b.WriteString("  ")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E")).Render("(Response pending)"))
			b.WriteString("\n")
			return b.String()
		}

		// Format JSON from Raw field based on prettyJSON toggle
		var jsonObj interface{}
		if err := json.Unmarshal([]byte(displayMsg.Raw), &jsonObj); err == nil {
			if m.prettyJSON {
				// Pretty print with indentation
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = displayMsg.Raw
				}
			} else {
				// Compact JSON (single line)
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = displayMsg.Raw
				}
			}
		} else {
			jsonStr = displayMsg.Raw
		}
	} else {
		// Fallback for unknown type
		contentLabel = "RAW JSON"
		jsonStr = "{}"
	}

	// Simple header with hints
	wrapStatus := "OFF"
	if m.jsonWrap {
		wrapStatus = "ON"
	}
	headerLabel := labelStyle.Bold(true).Render(contentLabel)
	header := fmt.Sprintf("%s  %s  %s",
		headerLabel,
		hintStyle.Render("[Tab:Format]"),
		hintStyle.Render(fmt.Sprintf("[w:Wrap=%s]", wrapStatus)))
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Split into lines and apply scrolling
	lines := strings.Split(jsonStr, "\n")
	// Calculate how many lines we can show - simplified calculation
	maxLines := max(1, m.height-18)
	start := m.detailScroll
	end := min(start+maxLines, len(lines))

	// Basic syntax highlighting styles
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5F87FF"))    // Blue for keys
	stringStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787")) // Green for strings
	numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")) // Yellow for numbers
	boolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8700"))   // Orange for booleans
	nullStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))   // Gray for null
	defaultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))

	for i := start; i < end; i++ {
		line := lines[i]
		maxWidth := m.width - 4 // Account for indentation

		// Handle wrapping - always wrap when pretty printing is enabled OR when explicitly enabled
		shouldWrap := m.jsonWrap || m.prettyJSON
		if shouldWrap {
			// Wrap long lines
			if len(line) > maxWidth {
				wrapped := wrapText(line, maxWidth)
				for _, wrapLine := range wrapped {
					b.WriteString("  ")
					b.WriteString(m.highlightJSON(wrapLine, keyStyle, stringStyle, numberStyle, boolStyle, nullStyle, defaultStyle))
					b.WriteString("\n")
				}
				continue
			}
		} else {
			// Truncate long lines only when not pretty-printing
			if len(line) > maxWidth {
				line = line[:max(0, maxWidth-3)] + "..."
			}
		}

		b.WriteString("  ")
		b.WriteString(m.highlightJSON(line, keyStyle, stringStyle, numberStyle, boolStyle, nullStyle, defaultStyle))
		b.WriteString("\n")
	}

	return b.String()
}

// reconstructLLMJSON creates a JSON representation from LLM event fields
// Returns compact JSON (without indentation) - caller can pretty print if needed
func (m *model) reconstructLLMJSON(evt *event.LLMEvent) string {
	// Create a simple JSON structure with available fields
	jsonMap := make(map[string]interface{})

	jsonMap["session_id"] = evt.SessionID
	jsonMap["timestamp"] = evt.Timestamp.Format("2006-01-02T15:04:05.000Z07:00")
	jsonMap["message_type"] = string(evt.MessageType)
	jsonMap["pid"] = evt.PID
	jsonMap["comm"] = evt.Comm
	jsonMap["host"] = evt.Host

	if evt.Model != "" {
		jsonMap["model"] = evt.Model
	}

	if evt.Path != "" {
		jsonMap["path"] = evt.Path
	}

	if evt.Content != "" {
		jsonMap["content"] = evt.Content
	}

	if evt.Error != "" {
		jsonMap["error"] = evt.Error
	}

	// Marshal to compact JSON (without indentation)
	jsonBytes, err := json.Marshal(jsonMap)
	if err != nil {
		return "{}"
	}

	return string(jsonBytes)
}

// getMaxDetailScroll calculates the maximum scroll position for the detail view
func (m *model) getMaxDetailScroll() int {
	filteredMsgs := m.getFilteredMessages()
	if len(filteredMsgs) == 0 {
		return 0
	}

	msg := filteredMsgs[m.selectedIndex]

	// Get JSON string based on message source
	var jsonStr string

	if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		// LLM events - use raw JSON from HTTP payload
		rawJSON := msg.llmEvent.RawJSON
		if rawJSON == "" {
			// Fallback for stream chunks
			rawJSON = m.reconstructLLMJSON(msg.llmEvent)
		}
		// Apply formatting based on prettyJSON toggle
		var jsonObj interface{}
		if err := json.Unmarshal([]byte(rawJSON), &jsonObj); err == nil {
			if m.prettyJSON {
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = rawJSON
				}
			} else {
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = rawJSON
				}
			}
		} else {
			jsonStr = rawJSON
		}
	} else if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		// MCP events - use raw JSON with tab logic
		var displayMsg *event.MCPEvent

		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			displayMsg = msg.mcpEvent
		} else {
			if m.detailViewTab == "request" {
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else if msg.mcpEvent.Request != nil {
						displayMsg = &event.MCPEvent{
							JSONRPCMessage: *msg.mcpEvent.Request,
						}
						if rawBytes, err := json.Marshal(msg.mcpEvent.Request); err == nil {
							displayMsg.Raw = string(rawBytes)
						}
					} else {
						displayMsg = msg.mcpEvent
					}
				}
			} else {
				if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
					displayMsg = msg.mcpEvent
				} else if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
					paired := m.findPairedMessage(msg)
					if paired != nil {
						displayMsg = paired
					} else {
						return 0 // No response yet
					}
				}
			}
		}

		if displayMsg == nil {
			return 0
		}

		// Format JSON the same way as renderRawJSON
		var jsonObj interface{}
		if err := json.Unmarshal([]byte(displayMsg.Raw), &jsonObj); err == nil {
			if m.prettyJSON {
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = displayMsg.Raw
				}
			} else {
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = displayMsg.Raw
				}
			}
		} else {
			jsonStr = displayMsg.Raw
		}
	} else {
		return 0
	}

	// Count lines
	lines := strings.Split(jsonStr, "\n")
	lineCount := len(lines)

	// Calculate available space (same as in renderRawJSON)
	maxLines := max(1, m.height-18)

	// Maximum scroll is the difference, or 0 if content fits
	return max(0, lineCount-maxLines)
}

// highlightJSON applies basic syntax highlighting to a JSON line
// Handles both pretty-printed and inline JSON
func (m *model) highlightJSON(line string, keyStyle, stringStyle, numberStyle, boolStyle, nullStyle, defaultStyle lipgloss.Style) string {
	var result strings.Builder
	i := 0

	for i < len(line) {
		// Skip structural characters
		if line[i] == '{' || line[i] == '}' || line[i] == '[' || line[i] == ']' || line[i] == ',' {
			result.WriteString(defaultStyle.Render(string(line[i])))
			i++
			continue
		}

		// Skip whitespace
		if line[i] == ' ' || line[i] == '\t' || line[i] == '\n' || line[i] == '\r' {
			result.WriteByte(line[i])
			i++
			continue
		}

		// Check for key-value pair: "key":
		if line[i] == '"' {
			// Find the closing quote
			start := i
			i++ // Skip opening quote
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++ // Skip escaped character
				}
				i++
			}
			if i < len(line) {
				i++ // Include closing quote

				// Check if this is a key (followed by colon)
				j := i
				for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
					j++ // Skip whitespace
				}

				if j < len(line) && line[j] == ':' {
					// This is a key
					result.WriteString(keyStyle.Render(line[start:i]))
					i = j
					result.WriteString(defaultStyle.Render(":"))
					i++ // Skip colon
					continue
				} else {
					// This is a string value
					result.WriteString(stringStyle.Render(line[start:i]))
					continue
				}
			} else {
				// Unterminated string - just render the rest and break
				result.WriteString(stringStyle.Render(line[start:]))
				break
			}
		}

		// Check for numbers
		if (line[i] >= '0' && line[i] <= '9') || line[i] == '-' {
			start := i
			if line[i] == '-' {
				i++
			}
			for i < len(line) && ((line[i] >= '0' && line[i] <= '9') || line[i] == '.') {
				i++
			}
			result.WriteString(numberStyle.Render(line[start:i]))
			continue
		}

		// Check for booleans and null
		if i+4 <= len(line) && line[i:i+4] == "true" {
			result.WriteString(boolStyle.Render("true"))
			i += 4
			continue
		}
		if i+5 <= len(line) && line[i:i+5] == "false" {
			result.WriteString(boolStyle.Render("false"))
			i += 5
			continue
		}
		if i+4 <= len(line) && line[i:i+4] == "null" {
			result.WriteString(nullStyle.Render("null"))
			i += 4
			continue
		}

		// Unknown character, just add it
		result.WriteByte(line[i])
		i++
	}

	return result.String()
}

// wrapText wraps text to the specified width
func wrapText(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}

	var lines []string
	for len(text) > width {
		// Try to break at a space
		breakPoint := width
		for i := width; i > 0; i-- {
			if text[i] == ' ' {
				breakPoint = i
				break
			}
		}
		lines = append(lines, text[:breakPoint])
		text = text[breakPoint:]
		if len(text) > 0 && text[0] == ' ' {
			text = text[1:] // Skip leading space
		}
	}
	if len(text) > 0 {
		lines = append(lines, text)
	}
	return lines
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// getPairingKey generates a unique key for request-response pairing
// The key includes transport type and session info to avoid cross-pairing
func getPairingKey(msg *event.MCPEvent) string {
	if msg.ID == nil {
		return ""
	}

	idStr := fmt.Sprintf("%v", msg.ID)

	switch msg.TransportType {
	case event.TransportTypeStdio:
		// For STDIO, create a bidirectional session key by sorting PIDs
		// This ensures request (A→B) and response (B→A) have the same key
		pid1, pid2 := msg.FromPID, msg.ToPID
		if pid1 > pid2 {
			pid1, pid2 = pid2, pid1
		}
		return fmt.Sprintf("stdio:%d:%d:%s", pid1, pid2, idStr)
	case event.TransportTypeHTTP:
		// For HTTP, include host and PID to identify the session
		if msg.HttpTransport != nil {
			return fmt.Sprintf("http:%s:%d:%s", msg.HttpTransport.Host, msg.HttpTransport.PID, idStr)
		}
		return fmt.Sprintf("http:unknown:%s", idStr)
	default:
		return fmt.Sprintf("%s:%s", msg.TransportType, idStr)
	}
}

// findPairedMessage finds the paired message (request for response, response for request)
func (m *model) findPairedMessage(msg *displayMessage) *event.MCPEvent {
	// Only MCP events have pairing
	if msg.source != sourceTypeMCP || msg.mcpEvent == nil {
		return nil
	}

	if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
		return nil // Notifications have no pairs
	}

	pairingKey := getPairingKey(msg.mcpEvent)
	if pairingKey == "" {
		return nil
	}

	if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
		// For responses, find the request with matching pairing key
		for _, m := range m.messages {
			if m.source == sourceTypeMCP && m.mcpEvent != nil && m.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
				if getPairingKey(m.mcpEvent) == pairingKey {
					return m.mcpEvent
				}
			}
		}
		return nil
	}

	// For requests, look up response in the map using the pairing key
	if response, ok := m.requestToResponse[pairingKey]; ok {
		return response
	}
	return nil
}

// isPairHighlighted checks if this message's pair is currently selected
func (m *model) isPairHighlighted(msg *displayMessage) bool {
	// Only MCP events have pairing
	if msg.source != sourceTypeMCP || msg.mcpEvent == nil {
		return false
	}

	paired := m.findPairedMessage(msg)
	if paired == nil {
		return false
	}

	// Check if paired message is the currently selected one
	filteredMsgs := m.getFilteredMessages()
	if m.selectedIndex >= 0 && m.selectedIndex < len(filteredMsgs) {
		selectedMsg := filteredMsgs[m.selectedIndex]
		if selectedMsg.source == sourceTypeMCP && selectedMsg.mcpEvent != nil {
			return selectedMsg.mcpEvent == paired
		}
	}
	return false
}
