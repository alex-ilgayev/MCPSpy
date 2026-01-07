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
	"github.com/muesli/reflow/ansi"
)

const (
	maxMessages = 1000
)

// messageSource distinguishes between MCP and LLM events (internal to TUI)
type messageSource int

const (
	sourceTypeMCP messageSource = iota
	sourceTypeLLM
	sourceTypeTool
)

// displayMessage wraps either MCP, LLM, or Tool events for rendering
type displayMessage struct {
	source    messageSource
	mcpEvent  *event.MCPEvent
	llmEvent  *event.LLMEvent
	toolEvent *event.ToolUsageEvent
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
	filterApp         string // "ALL", "MCP", "LLM", "TOOL"
	jsonWrap          bool
	density           densityMode
	requestToResponse   map[string]*event.MCPEvent      // Maps request key to response message
	invocationToResult  map[string]*event.ToolUsageEvent // Maps tool ID to result message
	detailViewTab       string                           // "request" or "response" (also "invocation" or "result" for tools)
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
		requestToResponse:  make(map[string]*event.MCPEvent),
		invocationToResult: make(map[string]*event.ToolUsageEvent),
		detailViewTab:      "request",
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

	// Subscribe to Tool usage events
	if err := eventBus.Subscribe(event.EventTypeToolUsage, d.handleToolMessage); err != nil {
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

// handleToolMessage receives Tool usage events and sends them to the TUI
func (d *TUIDisplay) handleToolMessage(e event.Event) {
	msg, ok := e.(*event.ToolUsageEvent)
	if !ok {
		return
	}

	// Wrap in displayMessage and send to Bubbletea program
	d.program.Send(msgReceived{msg: &displayMessage{
		source:    sourceTypeTool,
		toolEvent: msg,
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

				// Set default tab based on message type
				if selectedMsg.source == sourceTypeMCP && selectedMsg.mcpEvent != nil {
					if selectedMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeRequest {
						m.detailViewTab = "request"
					} else if selectedMsg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
						m.detailViewTab = "response"
					} else {
						// Notifications don't have tabs
						m.detailViewTab = "request"
					}
				} else if selectedMsg.source == sourceTypeTool && selectedMsg.toolEvent != nil {
					// Tool events have invocation/result tabs
					if selectedMsg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
						m.detailViewTab = "request" // "request" = invocation
					} else {
						m.detailViewTab = "response" // "response" = result
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
				m.filterApp = "TOOL"
			case "TOOL":
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
			// Switch between request/response tabs (MCP) or invocation/result tabs (Tool)
			filteredMsgs := m.getFilteredMessages()
			if len(filteredMsgs) > 0 {
				selectedMsg := filteredMsgs[m.selectedIndex]
				// MCP events
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
				// Tool events
				if selectedMsg.source == sourceTypeTool && selectedMsg.toolEvent != nil {
					if m.detailViewTab == "request" {
						// Check if result exists
						paired := m.findPairedToolMessage(selectedMsg)
						if paired != nil || selectedMsg.toolEvent.UsageType == event.ToolUsageTypeResult {
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
			if m.filterApp == "TOOL" && msg.source != sourceTypeTool {
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

// messagePassesFilters checks if a single message would pass the current filters
func (m *model) messagePassesFilters(msg *displayMessage) bool {
	// App filter
	if m.filterApp != "ALL" {
		if m.filterApp == "MCP" && msg.source != sourceTypeMCP {
			return false
		}
		if m.filterApp == "LLM" && msg.source != sourceTypeLLM {
			return false
		}
		if m.filterApp == "TOOL" && msg.source != sourceTypeTool {
			return false
		}
	}

	// Transport filter
	if m.filterTransport != "ALL" {
		transportType := m.getTransportType(msg)
		if m.filterTransport == "HTTP" && transportType != "HTTP" {
			return false
		}
		if m.filterTransport == "STDIO" && transportType != "STDIO" {
			return false
		}
	}

	// Type filter
	if m.filterType != "ALL" {
		msgType := m.getMessageTypeString(msg)
		if msgType != m.filterType {
			return false
		}
	}

	return true
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
	case sourceTypeTool:
		return "HTTP" // Tool events are always via HTTP (LLM API)
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
	case sourceTypeTool:
		if msg.toolEvent == nil {
			return "UNKNOWN"
		}
		switch msg.toolEvent.UsageType {
		case event.ToolUsageTypeInvocation:
			return "CALL"
		case event.ToolUsageTypeResult:
			if msg.toolEvent.IsError {
				return "ERROR"
			}
			return "RSLT"
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

	// Update invocation-result mapping for Tool results
	if msg.source == sourceTypeTool && msg.toolEvent != nil {
		if msg.toolEvent.UsageType == event.ToolUsageTypeResult && msg.toolEvent.ToolID != "" {
			m.invocationToResult[msg.toolEvent.ToolID] = msg.toolEvent
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
		// Clean up invocation-result mapping if removing a Tool result
		if oldestMsg.source == sourceTypeTool && oldestMsg.toolEvent != nil {
			if oldestMsg.toolEvent.UsageType == event.ToolUsageTypeResult && oldestMsg.toolEvent.ToolID != "" {
				delete(m.invocationToResult, oldestMsg.toolEvent.ToolID)
			}
		}

		// Only adjust indices if the removed message was visible (passed filters)
		wasVisible := m.messagePassesFilters(oldestMsg)

		m.messages = m.messages[1:]

		if wasVisible {
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
			if m.selectedIndex > 0 {
				m.selectedIndex--
			}
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
func (m *model) getColumnWidths() (time, transport, app, msgType, op, from, to, details int) {
	// Minimum widths
	minTime := 12
	minTransport := 9
	minApp := 4  // "TOOL" is 4 chars
	minType := 6 // "NOTIFY" is 6 chars
	minOp := 20  // Method/tool name/API path
	minFrom := 15
	minTo := 15
	minDetails := 20

	// Fixed elements: prefix(2) + arrow(3) + spaces(8) = 13
	fixedWidth := 13

	availableWidth := m.width - fixedWidth
	if availableWidth < 80 {
		// Very narrow terminal - use minimum widths
		return minTime, minTransport, minApp, minType, minOp, minFrom, minTo, minDetails
	}

	// Allocate widths proportionally
	totalMin := minTime + minTransport + minApp + minType + minOp + minFrom + minTo + minDetails
	extraSpace := availableWidth - totalMin

	if extraSpace < 0 {
		// Terminal too narrow, use minimums
		return minTime, minTransport, minApp, minType, minOp, minFrom, minTo, minDetails
	}

	// Distribute extra space (prioritize details column, then OP)
	detailsExtra := (extraSpace * 35) / 100
	opExtra := (extraSpace * 25) / 100
	fromExtra := (extraSpace * 15) / 100
	toExtra := (extraSpace * 15) / 100
	remaining := extraSpace - detailsExtra - opExtra - fromExtra - toExtra

	return minTime, minTransport + remaining/3, minApp, minType + remaining/3,
		minOp + opExtra, minFrom + fromExtra, minTo + toExtra, minDetails + detailsExtra
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

	timeW, transportW, appW, typeW, opW, fromW, toW, _ := m.getColumnWidths()

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
	b.WriteString(padString("OP / MODEL", opW))
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
	// Check if this message's pair is selected (for subtle highlighting)
	isPairHighlighted := false
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		isPairHighlighted = m.isPairHighlighted(msg)
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		isPairHighlighted = m.isPairHighlighted(msg)
	}

	// Get dynamic column widths
	timeW, transportW, appW, typeW, opW, fromW, toW, _ := m.getColumnWidths()

	// Get timestamp
	var timestamp time.Time
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		timestamp = msg.mcpEvent.Timestamp
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		timestamp = msg.llmEvent.Timestamp
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		timestamp = msg.toolEvent.Timestamp
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
	switch msg.source {
	case sourceTypeMCP:
		appStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB")) // Sky Blue (unique)
		appStr = padString("MCP", appW)
	case sourceTypeLLM:
		appStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#DA70D6")) // Orchid/Pink (unique)
		appStr = padString("LLM", appW)
	case sourceTypeTool:
		appStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF00FF")) // Magenta (unique)
		appStr = padString("TOOL", appW)
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
	case "CALL":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00D7FF")) // Cyan
		typeStr = padString("CALL", typeW)
	case "RSLT":
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787")) // Green
		typeStr = padString("RSLT", typeW)
	default:
		typeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))
		typeStr = padString(msgTypeStr, typeW)
	}

	// OP - shows method/tool name/API path
	opStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAF00")) // Amber/gold color
	var opStr string
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		opStr = msg.mcpEvent.Method
		// For responses without method, try to get it from the correlated request
		if opStr == "" && msg.mcpEvent.MessageType == event.JSONRPCMessageTypeResponse {
			if pairedRequest := m.findPairedMessage(msg); pairedRequest != nil {
				opStr = pairedRequest.Method
			}
		}
		if opStr == "" {
			opStr = "-"
		}
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		opStr = msg.toolEvent.ToolName
		// For Task tool, append subagent_type if present (e.g., "Task/Explore")
		if subagentType := extractSubagentType(msg.toolEvent.Input); subagentType != "" {
			opStr += "/" + subagentType
		}
	} else if msg.source == sourceTypeLLM && msg.llmEvent != nil {
		// Show model for LLM
		opStr = msg.llmEvent.Model
		if opStr == "" {
			opStr = "-"
		}
	}
	opStr = padString(opStr, opW)

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
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Tool: invocation goes host→PID, result goes PID→host
		if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
			fromPlain := msg.toolEvent.Host
			toPlain := fmt.Sprintf("%s[%d]", msg.toolEvent.Comm, msg.toolEvent.PID)
			fromStr = padString(fromPlain, fromW)
			toStr = padString(toPlain, toW)
		} else {
			fromPlain := fmt.Sprintf("%s[%d]", msg.toolEvent.Comm, msg.toolEvent.PID)
			toPlain := msg.toolEvent.Host
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
		// LLM: show content only (model is in OP column)
		content := strings.ReplaceAll(msg.llmEvent.Content, "\n", " ") // Replace newlines with spaces
		if content != "" {
			detailsStr = fmt.Sprintf("\"%s\"", content)
		}
		// Trimming will happen below based on available width
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Tool: show input/output summary only (tool name is in OP column)
		if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
			detailsStr = formatToolInputForTUI(msg.toolEvent.ToolName, msg.toolEvent.Input)
		} else {
			if msg.toolEvent.IsError {
				errMsg := strings.ReplaceAll(msg.toolEvent.Output, "\n", " ")
				detailsStr = "ERROR: " + truncateStringForTUI(errMsg, 40)
			} else {
				output := strings.ReplaceAll(msg.toolEvent.Output, "\n", " ")
				detailsStr = truncateStringForTUI(output, 50)
			}
		}
	}

	// Calculate remaining space for details (to prevent overflow)
	// prefix(2) + timeW + transport + app + typeW + opW + fromW + arrow + toW + spaces(8)
	// Note: arrow "→" is 3 bytes in UTF-8 but displays as 1 character width
	arrowWidth := 1 // Display width of arrow
	usedWidth := 2 + timeW + 1 + transportW + 1 + appW + 1 + typeW + 1 + opW + 1 + fromW + 1 + arrowWidth + 1 + toW + 1
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
		b.WriteString(opStr)
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
			len(opStr) + 1 + len(fromStr) + 1 + 1 + 1 + len(toStr) + 1 + len(detailsStr)
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
		opStyleBg := opStyle.Background(bgColor)
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
		b.WriteString(opStyleBg.Render(opStr))
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
			len(opStr) + 1 + len(fromStr) + 1 + 1 + 1 + len(toStr) + 1 + len(detailsStr)
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
	b.WriteString(opStyle.Render(opStr))
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

	// Handle MCP events
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
		// Don't show tabs for notifications
		if msg.mcpEvent.MessageType == event.JSONRPCMessageTypeNotification {
			return ""
		}

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

	// Handle Tool events
	if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Determine which tabs are available
		hasResult := false
		resultLabel := "Result"

		paired := m.findPairedToolMessage(msg)
		if msg.toolEvent.UsageType == event.ToolUsageTypeResult {
			hasResult = true
			if msg.toolEvent.IsError {
				resultLabel = "Result (error)"
			}
		} else if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
			if paired != nil {
				hasResult = true
				// Check if result is an error
				if paired.IsError {
					resultLabel = "Result (error)"
				}
			} else {
				resultLabel = "Result (pending)"
			}
		}

		// Render invocation tab (with manual padding)
		if m.detailViewTab == "request" {
			b.WriteString(activeTabStyle.Render(" Invocation "))
		} else {
			b.WriteString(inactiveTabStyle.Render(" Invocation "))
		}

		b.WriteString(" ")

		// Render result tab (with manual padding)
		if !hasResult {
			b.WriteString(disabledTabStyle.Render(" " + resultLabel + " "))
		} else if m.detailViewTab == "response" {
			b.WriteString(activeTabStyle.Render(" " + resultLabel + " "))
		} else {
			b.WriteString(inactiveTabStyle.Render(" " + resultLabel + " "))
		}

		return b.String()
	}

	// LLM events don't have tabs
	return ""
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

	// Tab bar (for MCP and Tool events, not LLM)
	if (msg.source == sourceTypeMCP && msg.mcpEvent != nil) || (msg.source == sourceTypeTool && msg.toolEvent != nil) {
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
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Handle Tool events with tab support
		var displayTool *event.ToolUsageEvent

		// Determine which tool event to display based on active tab
		if m.detailViewTab == "request" {
			// Show invocation
			if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
				displayTool = msg.toolEvent
			} else {
				// We're on a result, show the paired invocation
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					displayTool = msg.toolEvent // Fallback
				}
			}
		} else {
			// Show result
			if msg.toolEvent.UsageType == event.ToolUsageTypeResult {
				displayTool = msg.toolEvent
			} else {
				// We're on an invocation, show the paired result
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					displayTool = nil // No result yet
				}
			}
		}

		// If no message to display (e.g., pending result)
		if displayTool == nil {
			renderField("Status:        ", "(Result pending)")
			return b.String()
		}

		renderField("Timestamp:     ", displayTool.Timestamp.Format("2006-01-02 15:04:05.000"))
		renderField("Transport:     ", "HTTP")
		renderField("App:           ", "TOOL")

		usageTypeStr := "Invocation"
		if displayTool.UsageType == event.ToolUsageTypeResult {
			usageTypeStr = "Result"
		}
		renderField("Usage Type:    ", usageTypeStr)
		renderField("Tool Name:     ", displayTool.ToolName)

		if displayTool.ToolID != "" {
			renderField("Tool ID:       ", displayTool.ToolID)
		} else {
			renderField("Tool ID:       ", "-")
		}

		if displayTool.IsError {
			renderField("Status:        ", "Error")
		}

		// From/To based on usage type
		if displayTool.UsageType == event.ToolUsageTypeInvocation {
			// Invocation: From Host → To Process
			renderField("From Host:     ", displayTool.Host)
			toStr := fmt.Sprintf("%s (PID: %d)", displayTool.Comm, displayTool.PID)
			renderField("To Process:    ", toStr)
		} else {
			// Result: From Process → To Host
			fromStr := fmt.Sprintf("%s (PID: %d)", displayTool.Comm, displayTool.PID)
			renderField("From Process:  ", fromStr)
			renderField("To Host:       ", displayTool.Host)
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
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Tool event - support tabs for invocation/result
		var displayTool *event.ToolUsageEvent
		var isInvocation bool

		// Determine which tool event to display based on active tab
		if m.detailViewTab == "request" {
			// Show invocation
			isInvocation = true
			if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
				displayTool = msg.toolEvent
			} else {
				// We're on a result, show the paired invocation
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					displayTool = msg.toolEvent // Fallback
				}
			}
		} else {
			// Show result
			isInvocation = false
			if msg.toolEvent.UsageType == event.ToolUsageTypeResult {
				displayTool = msg.toolEvent
			} else {
				// We're on an invocation, show the paired result
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					displayTool = nil // No result yet
				}
			}
		}

		// If no message to display (e.g., pending result)
		if displayTool == nil {
			contentLabel = "TOOL OUTPUT"
			// Simple header with hints (no format option for pending)
			wrapStatus := "OFF"
			if m.jsonWrap {
				wrapStatus = "ON"
			}
			headerLabel := labelStyle.Bold(true).Render(contentLabel)
			header := fmt.Sprintf("%s  %s",
				headerLabel,
				hintStyle.Render(fmt.Sprintf("[w:Wrap=%s]", wrapStatus)))
			b.WriteString(header)
			b.WriteString("\n")
			b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
			b.WriteString("\n")
			b.WriteString("  ")
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E")).Render("(Result pending)"))
			b.WriteString("\n")
			return b.String()
		}

		// Get the data string based on what we're showing
		var dataStr string
		if isInvocation {
			dataStr = displayTool.Input
		} else {
			dataStr = displayTool.Output
		}

		// Check if content is a JSON object or array (not just a valid JSON string)
		var jsonObj interface{}
		isStructuredJSON := false
		if json.Unmarshal([]byte(dataStr), &jsonObj) == nil {
			// Only consider it JSON if it's an object or array, not a primitive
			switch jsonObj.(type) {
			case map[string]interface{}, []interface{}:
				isStructuredJSON = true
			}
		}

		if isInvocation {
			if isStructuredJSON {
				contentLabel = "TOOL INPUT (JSON)"
			} else {
				contentLabel = "TOOL INPUT"
			}
		} else {
			if isStructuredJSON {
				contentLabel = "TOOL OUTPUT (JSON)"
			} else {
				contentLabel = "TOOL OUTPUT"
			}
		}

		// Apply formatting based on content type
		if isStructuredJSON {
			// JSON object/array - apply pretty print toggle
			if m.prettyJSON {
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = dataStr
				}
			} else {
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = dataStr
				}
			}
		} else {
			// Plain text - unescape JSON string if quoted, convert \n to newlines
			jsonStr = unescapeToolOutput(dataStr)
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
	// Only show format option for JSON content
	var header string
	if strings.Contains(contentLabel, "(JSON)") || strings.HasPrefix(contentLabel, "REQUEST") || strings.HasPrefix(contentLabel, "RESPONSE") {
		header = fmt.Sprintf("%s  %s  %s",
			headerLabel,
			hintStyle.Render("[Tab:Format]"),
			hintStyle.Render(fmt.Sprintf("[w:Wrap=%s]", wrapStatus)))
	} else {
		header = fmt.Sprintf("%s  %s",
			headerLabel,
			hintStyle.Render(fmt.Sprintf("[w:Wrap=%s]", wrapStatus)))
	}
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(sepStyle.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")

	// Split into lines and calculate display lines for proper scrolling
	lines := strings.Split(jsonStr, "\n")
	maxWidth := m.width - 4 // Account for indentation
	shouldWrap := m.jsonWrap || m.prettyJSON

	// Calculate how many display lines we can show
	viewportLines := max(1, m.height-18)

	// Basic syntax highlighting styles
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5F87FF"))    // Blue for keys
	stringStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787")) // Green for strings
	numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700")) // Yellow for numbers
	boolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8700"))   // Orange for booleans
	nullStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E"))   // Gray for null
	defaultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#D0D0D0"))

	// Build list of all display lines (after wrapping/highlighting)
	var displayLines []string
	for _, line := range lines {
		highlighted := m.highlightJSON(line, keyStyle, stringStyle, numberStyle, boolStyle, nullStyle, defaultStyle)

		if shouldWrap {
			if ansi.PrintableRuneWidth(highlighted) > maxWidth {
				wrapped := wrapANSIText(highlighted, maxWidth)
				for _, wrapLine := range wrapped {
					displayLines = append(displayLines, "  "+wrapLine)
				}
			} else {
				displayLines = append(displayLines, "  "+highlighted)
			}
		} else {
			if ansi.PrintableRuneWidth(highlighted) > maxWidth {
				highlighted = truncateANSI(highlighted, maxWidth-3) + "..."
			}
			displayLines = append(displayLines, "  "+highlighted)
		}
	}

	// Apply scrolling to display lines
	start := m.detailScroll
	end := min(start+viewportLines, len(displayLines))
	if start > len(displayLines) {
		start = max(0, len(displayLines)-viewportLines)
		end = len(displayLines)
	}

	for i := start; i < end; i++ {
		b.WriteString(displayLines[i])
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
	} else if msg.source == sourceTypeTool && msg.toolEvent != nil {
		// Tool events - support tabs for invocation/result
		var displayTool *event.ToolUsageEvent

		// Determine which tool event to display based on active tab
		if m.detailViewTab == "request" {
			// Show invocation
			if msg.toolEvent.UsageType == event.ToolUsageTypeInvocation {
				displayTool = msg.toolEvent
			} else {
				// We're on a result, show the paired invocation
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					displayTool = msg.toolEvent // Fallback
				}
			}
		} else {
			// Show result
			if msg.toolEvent.UsageType == event.ToolUsageTypeResult {
				displayTool = msg.toolEvent
			} else {
				// We're on an invocation, show the paired result
				paired := m.findPairedToolMessage(msg)
				if paired != nil {
					displayTool = paired
				} else {
					return 0 // No result yet
				}
			}
		}

		if displayTool == nil {
			return 0
		}

		// Get the data string based on what we're showing
		var dataStr string
		if m.detailViewTab == "request" {
			dataStr = displayTool.Input
		} else {
			dataStr = displayTool.Output
		}

		// Check if content is a JSON object or array (not just a valid JSON string)
		var jsonObj interface{}
		isStructuredJSON := false
		if json.Unmarshal([]byte(dataStr), &jsonObj) == nil {
			switch jsonObj.(type) {
			case map[string]interface{}, []interface{}:
				isStructuredJSON = true
			}
		}

		// Apply formatting based on content type
		if isStructuredJSON {
			if m.prettyJSON {
				if prettyBytes, err := json.MarshalIndent(jsonObj, "", "  "); err == nil {
					jsonStr = string(prettyBytes)
				} else {
					jsonStr = dataStr
				}
			} else {
				if compactBytes, err := json.Marshal(jsonObj); err == nil {
					jsonStr = string(compactBytes)
				} else {
					jsonStr = dataStr
				}
			}
		} else {
			// Plain text - unescape JSON string if quoted, convert \n to newlines
			jsonStr = unescapeToolOutput(dataStr)
		}
	} else {
		return 0
	}

	// Count display lines (after wrapping) - same logic as renderRawJSON
	lines := strings.Split(jsonStr, "\n")
	maxWidth := m.width - 4
	shouldWrap := m.jsonWrap || m.prettyJSON

	displayLineCount := 0
	for _, line := range lines {
		if shouldWrap {
			lineWidth := len(line) // Approximate width (exact would require highlighting first)
			if lineWidth > maxWidth && maxWidth > 0 {
				// Count how many lines this will wrap to
				displayLineCount += (lineWidth + maxWidth - 1) / maxWidth
			} else {
				displayLineCount++
			}
		} else {
			displayLineCount++
		}
	}

	// Calculate available space (same as in renderRawJSON)
	viewportLines := max(1, m.height-18)

	// Maximum scroll is the difference, or 0 if content fits
	return max(0, displayLineCount-viewportLines)
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

// wrapANSIText wraps text containing ANSI escape codes to the specified width.
// It preserves ANSI codes across line breaks so colors continue properly.
func wrapANSIText(text string, width int) []string {
	if ansi.PrintableRuneWidth(text) <= width {
		return []string{text}
	}

	var lines []string
	var currentLine strings.Builder
	var currentWidth int
	var activeSequence string // Track the current active ANSI sequence

	i := 0
	runes := []rune(text)

	for i < len(runes) {
		// Check for ANSI escape sequence
		if i < len(runes) && runes[i] == '\x1b' {
			// Find the end of the escape sequence
			seqStart := i
			i++
			if i < len(runes) && runes[i] == '[' {
				i++
				for i < len(runes) && !((runes[i] >= 'A' && runes[i] <= 'Z') || (runes[i] >= 'a' && runes[i] <= 'z')) {
					i++
				}
				if i < len(runes) {
					i++ // Include the final letter
				}
				seq := string(runes[seqStart:i])
				currentLine.WriteString(seq)

				// Track active sequence (reset or color)
				if seq == "\x1b[0m" || seq == "\x1b[m" {
					activeSequence = ""
				} else {
					activeSequence = seq
				}
				continue
			}
		}

		// Regular character - check width
		charWidth := ansi.PrintableRuneWidth(string(runes[i]))
		if currentWidth+charWidth > width {
			// Need to wrap - close the line and start a new one
			if activeSequence != "" {
				currentLine.WriteString("\x1b[0m") // Reset at end of line
			}
			lines = append(lines, currentLine.String())
			currentLine.Reset()
			currentWidth = 0
			if activeSequence != "" {
				currentLine.WriteString(activeSequence) // Restore active color
			}
		}

		currentLine.WriteRune(runes[i])
		currentWidth += charWidth
		i++
	}

	if currentLine.Len() > 0 {
		lines = append(lines, currentLine.String())
	}

	return lines
}

// truncateANSI truncates text containing ANSI escape codes to the specified width.
func truncateANSI(text string, width int) string {
	if ansi.PrintableRuneWidth(text) <= width {
		return text
	}

	var result strings.Builder
	var currentWidth int

	i := 0
	runes := []rune(text)

	for i < len(runes) && currentWidth < width {
		// Check for ANSI escape sequence
		if runes[i] == '\x1b' {
			seqStart := i
			i++
			if i < len(runes) && runes[i] == '[' {
				i++
				for i < len(runes) && !((runes[i] >= 'A' && runes[i] <= 'Z') || (runes[i] >= 'a' && runes[i] <= 'z')) {
					i++
				}
				if i < len(runes) {
					i++
				}
				result.WriteString(string(runes[seqStart:i]))
				continue
			}
		}

		charWidth := ansi.PrintableRuneWidth(string(runes[i]))
		if currentWidth+charWidth > width {
			break
		}
		result.WriteRune(runes[i])
		currentWidth += charWidth
		i++
	}

	result.WriteString("\x1b[0m") // Reset at end
	return result.String()
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
	// MCP events pairing
	if msg.source == sourceTypeMCP && msg.mcpEvent != nil {
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

	// Tool events pairing
	if msg.source == sourceTypeTool && msg.toolEvent != nil {
		paired := m.findPairedToolMessage(msg)
		if paired == nil {
			return false
		}

		// Check if paired message is the currently selected one
		filteredMsgs := m.getFilteredMessages()
		if m.selectedIndex >= 0 && m.selectedIndex < len(filteredMsgs) {
			selectedMsg := filteredMsgs[m.selectedIndex]
			if selectedMsg.source == sourceTypeTool && selectedMsg.toolEvent != nil {
				return selectedMsg.toolEvent == paired
			}
		}
		return false
	}

	return false
}

// findPairedToolMessage finds the paired invocation/result for a tool event
func (m *model) findPairedToolMessage(msg *displayMessage) *event.ToolUsageEvent {
	if msg.source != sourceTypeTool || msg.toolEvent == nil {
		return nil
	}

	toolID := msg.toolEvent.ToolID
	if toolID == "" {
		return nil
	}

	if msg.toolEvent.UsageType == event.ToolUsageTypeResult {
		// For results, find the invocation with matching tool ID
		for _, m := range m.messages {
			if m.source == sourceTypeTool && m.toolEvent != nil && m.toolEvent.UsageType == event.ToolUsageTypeInvocation {
				if m.toolEvent.ToolID == toolID {
					return m.toolEvent
				}
			}
		}
		return nil
	}

	// For invocations, look up result in the map using the tool ID
	if result, ok := m.invocationToResult[toolID]; ok {
		return result
	}
	return nil
}

// extractSubagentType extracts subagent_type from tool input JSON if present
func extractSubagentType(input string) string {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	if subagentType, ok := params["subagent_type"].(string); ok {
		return subagentType
	}
	return ""
}

// formatToolInputForTUI formats tool invocation input for TUI display
func formatToolInputForTUI(toolName, input string) string {
	var params map[string]interface{}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return truncateStringForTUI(input, 50)
	}

	// Extract the most relevant parameter based on common patterns
	switch {
	case toolName == "Read" || toolName == "Write" || toolName == "Edit":
		if path, ok := params["file_path"].(string); ok {
			return truncateStringForTUI(path, 50)
		}
	case toolName == "Bash":
		if cmd, ok := params["command"].(string); ok {
			return truncateStringForTUI(cmd, 50)
		}
	case toolName == "Glob":
		if pattern, ok := params["pattern"].(string); ok {
			return pattern
		}
	case toolName == "Grep":
		if pattern, ok := params["pattern"].(string); ok {
			return fmt.Sprintf("/%s/", pattern)
		}
	case toolName == "Task":
		if desc, ok := params["description"].(string); ok {
			return truncateStringForTUI(desc, 50)
		}
	}

	// Fallback: show compact JSON
	return truncateStringForTUI(input, 50)
}

// truncateStringForTUI truncates a string for TUI display
func truncateStringForTUI(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

// unescapeToolOutput converts a potentially JSON-quoted string to plain text
// If the string is a JSON string (starts/ends with quotes), it unescapes it
// This converts \n to actual newlines, \t to tabs, etc.
func unescapeToolOutput(s string) string {
	// Check if it looks like a JSON-quoted string
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		// Try to unmarshal as a JSON string
		var unescaped string
		if err := json.Unmarshal([]byte(s), &unescaped); err == nil {
			return unescaped
		}
	}
	// Not a JSON string, return as-is
	return s
}
