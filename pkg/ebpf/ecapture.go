package ebpf

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/alex-ilgayev/mcpspy/internal/embed"
	"github.com/sirupsen/logrus"
)

// ecaptureManager manages the ecapture subprocess and event collection
type ecaptureManager struct {
	cmd          *exec.Cmd
	listener     net.Listener
	eventCh      chan<- Event
	httpParser   *httpParser
	ecapturePath string // Path to ecapture binary (embedded or external)
	isEmbedded   bool   // Whether we're using embedded binary
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
	// Listen on a random available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create TCP listener: %w", err)
	}

	em := &ecaptureManager{
		listener:   listener,
		eventCh:    eventCh,
		httpParser: newHTTPParser(),
	}

	// Check if we have an embedded ecapture binary
	if embed.HasEmbeddedEcapture() {
		ecapturePath, err := embed.ExtractEcapture()
		if err != nil {
			listener.Close()
			return nil, fmt.Errorf("failed to extract embedded ecapture: %w", err)
		}
		em.ecapturePath = ecapturePath
		em.isEmbedded = true
		logrus.Info("Using embedded ecapture binary")
	} else {
		// Fall back to external ecapture binary
		em.ecapturePath = "./bpf/ecapture/bin/ecapture"
		em.isEmbedded = false

		// Check if external binary exists
		if _, err := os.Stat(em.ecapturePath); err != nil {
			listener.Close()
			return nil, fmt.Errorf("ecapture binary not found at %s and no embedded binary available", em.ecapturePath)
		}
		logrus.Info("Using external ecapture binary")
	}

	return em, nil
}

// start launches the ecapture process and begins listening for events
func (em *ecaptureManager) start(ctx context.Context) error {
	// Get the actual port we're listening on
	addr := em.listener.Addr().(*net.TCPAddr)
	eventAddr := fmt.Sprintf("tcp://127.0.0.1:%d", addr.Port)

	logrus.Infof("Starting ecapture with event address: %s", eventAddr)

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

	logrus.Infof("Started ecapture process with PID: %d", em.cmd.Process.Pid)

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
		Timestamp: time.Now(),
		Headers:   make(map[string]string),
		RawData:   []byte(data),
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
		logrus.Info("Terminating ecapture process")
		if err := em.cmd.Process.Kill(); err != nil {
			errs = append(errs, fmt.Errorf("failed to kill ecapture process: %w", err))
		}
	}

	// Clean up embedded binary if used
	if em.isEmbedded && em.ecapturePath != "" {
		if err := embed.CleanupEcapture(em.ecapturePath); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup embedded ecapture: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during ecapture cleanup: %v", errs)
	}

	return nil
}
