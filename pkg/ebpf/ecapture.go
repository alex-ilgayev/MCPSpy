package ebpf

import (
	_ "embed"
	"path/filepath"

	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// ecaptureManager manages the ecapture subprocess and event collection
type ecaptureManager struct {
	cmd          *exec.Cmd
	listener     net.Listener
	eventCh      chan<- Event
	httpParser   *httpParser
	ecapturePath string // Path to ecapture binary
}

// httpParser parses HTTP data from captured TLS plaintext
type httpParser struct {
	httpRequestRegex  *regexp.Regexp
	httpResponseRegex *regexp.Regexp
	headerRegex       *regexp.Regexp
}

func newHTTPParser() *httpParser {
	return &httpParser{
		httpRequestRegex:  regexp.MustCompile(`^(GET|POST|PUT|DELETE|HEAD|OPTIONS|PATCH)\s+([^\s]+)\s+HTTP/1\.[01]`),
		httpResponseRegex: regexp.MustCompile(`^HTTP/1\.[01]\s+(\d+)\s+(.+)`),
		headerRegex:       regexp.MustCompile(`^([^:]+):\s*(.+)$`),
	}
}

// newEcaptureManager creates a new ecapture manager
func newEcaptureManager(eventCh chan<- Event) (*ecaptureManager, error) {
	em := &ecaptureManager{
		eventCh:    eventCh,
		httpParser: newHTTPParser(),
	}

	// Check if we have an embedded ecapture binary
	if hasEmbeddedEcapture() {
		ecapturePath, err := extractEcapture()
		if err != nil {
			return nil, fmt.Errorf("failed to extract embedded ecapture: %w", err)
		}
		em.ecapturePath = ecapturePath
	} else {
		return nil, fmt.Errorf("ecapture binary not found")
	}

	return em, nil
}

// start launches the ecapture process and begins listening for events
func (em *ecaptureManager) start(ctx context.Context) error {
	var err error

	// Listen on a random available port
	em.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to create TCP listener for ecapture: %w", err)
	}

	addr := em.listener.Addr().(*net.TCPAddr)
	eventAddr := fmt.Sprintf("tcp://127.0.0.1:%d", addr.Port)

	logrus.Debugf("Starting ecapture with event address: %s", eventAddr)

	// Build ecapture command
	em.cmd = exec.CommandContext(ctx,
		em.ecapturePath,
		"tls",
		"-m", "text",
		"--eventaddr", eventAddr,
		"--listen", "", // Disable HTTP server
	)

	// Start ecapture
	if err := em.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ecapture: %w", err)
	}

	logrus.Debugf("Started ecapture process with PID: %d", em.cmd.Process.Pid)

	// Start accepting connections
	go em.acceptConnections(ctx)

	// Wait for ecapture to exit
	go func() {
		if err := em.cmd.Wait(); err != nil && ctx.Err() == nil {
			logrus.WithError(err).Error("ecapture process exited with error")
		}
	}()

	return nil
}

// acceptConnections handles incoming connections from ecapture
func (em *ecaptureManager) acceptConnections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, err := em.listener.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logrus.WithError(err).Error("Failed to accept connection")
				continue
			}

			logrus.Debugf("Accepted connection from ecapture: %s", conn.RemoteAddr())
			go em.handleConnection(ctx, conn)
		}
	}
}

// handleConnection processes events from a single ecapture connection
func (em *ecaptureManager) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 64*1024) // Increase buffer size for large HTTP payloads

	var buffer strings.Builder

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()

			fmt.Println(line)

			// Check if this is the start of a new HTTP request/response
			if em.isHTTPStart(line) && buffer.Len() > 0 {
				// Process the previous buffer
				em.processHTTPData(buffer.String())
				buffer.Reset()
			}

			buffer.WriteString(line)
			buffer.WriteString("\n")
		}
	}

	// Process any remaining data
	if buffer.Len() > 0 {
		em.processHTTPData(buffer.String())
	}

	if err := scanner.Err(); err != nil {
		logrus.WithError(err).Error("Error reading from ecapture connection")
	}
}

// isHTTPStart checks if a line appears to be the start of an HTTP request/response
func (em *ecaptureManager) isHTTPStart(line string) bool {
	return strings.HasPrefix(line, "GET ") ||
		strings.HasPrefix(line, "POST ") ||
		strings.HasPrefix(line, "PUT ") ||
		strings.HasPrefix(line, "DELETE ") ||
		strings.HasPrefix(line, "HEAD ") ||
		strings.HasPrefix(line, "OPTIONS ") ||
		strings.HasPrefix(line, "PATCH ") ||
		strings.HasPrefix(line, "HTTP/1.")
}

// processHTTPData parses HTTP data and sends it as an event
func (em *ecaptureManager) processHTTPData(data string) {
	event := em.httpParser.parseHTTPData(data)
	if event != nil {
		select {
		case em.eventCh <- event:
		default:
			logrus.Warn("Event channel full, dropping HTTP event")
		}
	}
}

// parseHTTPData parses raw HTTP data into an HTTPPayload event
func (p *httpParser) parseHTTPData(data string) *HTTPPayload {
	lines := strings.Split(data, "\n")
	if len(lines) == 0 {
		return nil
	}

	firstLine := strings.TrimSpace(lines[0])

	// Base event
	event := &HTTPPayload{
		EventHeader: EventHeader{
			EventType: EventTypeHTTP,
			// PID and Comm will be filled by ecapture logs if available
		},
		Headers: make(map[string]string),
	}

	// Check if it's an HTTP request
	if matches := p.httpRequestRegex.FindStringSubmatch(firstLine); matches != nil {
		event.IsRequest = true
		event.Method = matches[1]
		event.URL = matches[2]
		p.parseHeaders(lines[1:], event)
		return event
	}

	// Check if it's an HTTP response
	if matches := p.httpResponseRegex.FindStringSubmatch(firstLine); matches != nil {
		event.IsRequest = false
		statusCode, _ := strconv.Atoi(matches[1])
		event.StatusCode = statusCode
		p.parseHeaders(lines[1:], event)
		return event
	}

	return nil
}

// parseHeaders extracts headers and body from HTTP lines
func (p *httpParser) parseHeaders(lines []string, event *HTTPPayload) {
	bodyStartIndex := -1

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			bodyStartIndex = i + 1
			break
		}

		if matches := p.headerRegex.FindStringSubmatch(line); matches != nil {
			event.Headers[strings.ToLower(matches[1])] = matches[2]
		}
	}

	// Extract body if present
	if bodyStartIndex >= 0 && bodyStartIndex < len(lines) {
		bodyLines := lines[bodyStartIndex:]
		body := strings.Join(bodyLines, "\n")
		event.Body = []byte(strings.TrimSpace(body))
	}
}

// stop terminates the ecapture process
func (em *ecaptureManager) stop() error {
	var errs []error

	if em.listener != nil {
		em.listener.Close()
	}

	if em.cmd != nil && em.cmd.Process != nil {
		logrus.Debug("Terminating ecapture process")
		if err := em.cmd.Process.Kill(); err != nil {
			errs = append(errs, fmt.Errorf("failed to kill ecapture process: %w", err))
		}
	}

	// Clean up extracted embedded binary
	if err := cleanupEcapture(em.ecapturePath); err != nil {
		errs = append(errs, fmt.Errorf("failed to cleanup embedded ecapture: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during ecapture cleanup: %v", errs)
	}

	return nil
}

// Embed the ecapture binary
//
//go:embed ecapture
var ecaptureBinary []byte

// hasEmbeddedEcapture returns true if ecapture is embedded
func hasEmbeddedEcapture() bool {
	return len(ecaptureBinary) > 0
}

// extractEcapture extracts the embedded ecapture binary to a temporary location
// and returns the path to the executable
func extractEcapture() (string, error) {
	if len(ecaptureBinary) == 0 {
		return "", fmt.Errorf("ecapture binary not embedded")
	}

	// Create a temporary directory for the binary
	tmpDir, err := os.MkdirTemp("", "mcpspy-ecapture-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Path for the extracted binary
	ecapturePath := filepath.Join(tmpDir, "ecapture")

	// Write the binary to disk
	if err := os.WriteFile(ecapturePath, ecaptureBinary, 0755); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to write ecapture binary: %w", err)
	}

	logrus.Debugf("Extracted embedded ecapture to: %s", ecapturePath)
	return ecapturePath, nil
}

// cleanupEcapture removes the temporary directory containing the ecapture binary
func cleanupEcapture(ecapturePath string) error {
	if ecapturePath == "" {
		return nil
	}

	// Get the directory containing the binary
	dir := filepath.Dir(ecapturePath)

	// Remove the entire temporary directory
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to cleanup ecapture: %w", err)
	}

	logrus.Debug("Cleaned up ecapture temporary files")
	return nil
}
