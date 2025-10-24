package http

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/alex-ilgayev/mcpspy/pkg/bus"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
	"github.com/sirupsen/logrus"
)

// httpRequest represents a parsed HTTP request
// We do not include this in `Event` type, so we'll be able to change it freely
type httpRequest struct {
	isComplete bool
	method     string
	path       string
	host       string
	headers    map[string]string
	body       []byte
}

// httpResponse represents a parsed HTTP response
// We do not include this in `Event` type, so we'll be able to change it freely
type httpResponse struct {
	isComplete bool
	statusCode int
	headers    map[string]string
	body       []byte
	isChunked  bool
	isSSE      bool
}

// session tracks HTTP communication for a single SSL context
type session struct {
	sslContext uint64

	pid  uint32
	comm [16]uint8

	request    *httpRequest
	requestBuf *bytes.Buffer

	response    *httpResponse
	responseBuf *bytes.Buffer

	// Event emission tracking
	requestEventEmitted  bool
	responseEventEmitted bool

	// SSE tracking
	isSSE         bool
	sseEventsSent int // Track how many SSE events we've already sent
}

func (s *session) logFields() logrus.Fields {
	return logrus.Fields{
		"ssl_ctx": s.sslContext,
		"pid":     s.pid,
		"comm":    strings.TrimRight(string(s.comm[:]), "\x00"),
	}
}

// SessionManager manages HTTP sessions over SSL contexts
// Subscribes to the following events:
// - TlsPayload (for both send and recv) - to capture HTTP data
// - TlsFree - to clean up sessions
//
// Emits the following events:
// - HttpRequestEvent
// - HttpResponseEvent
// - SSEEvent
type SessionManager struct {
	mu       sync.Mutex
	sessions map[uint64]*session // key is SSL context
	eventBus bus.EventBus
}

func NewSessionManager(eventBus bus.EventBus) (*SessionManager, error) {
	sm := &SessionManager{
		sessions: make(map[uint64]*session),
		eventBus: eventBus,
	}

	if err := eventBus.Subscribe(event.EventTypeTlsPayloadRecv, sm.ProcessTlsEvent); err != nil {
		return nil, err
	}
	if err := eventBus.Subscribe(event.EventTypeTlsPayloadSend, sm.ProcessTlsEvent); err != nil {
		sm.Close()
		return nil, err
	}
	if err := eventBus.Subscribe(event.EventTypeTlsFree, sm.ProcessTlsFreeEvent); err != nil {
		sm.Close()
		return nil, err
	}

	return sm, nil
}

func (s *SessionManager) ProcessTlsEvent(e event.Event) {
	// We only handle TlsPayload events here
	tlsEvent, ok := e.(*event.TlsPayloadEvent)
	if !ok {
		return
	}

	// Only process HTTP/1.1 events for now.
	if tlsEvent.HttpVersion != event.HttpVersion1 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	logrus.WithFields(e.LogFields()).Trace("Processing TLS event")

	// Get or create session
	sess, exists := s.sessions[tlsEvent.SSLContext]
	if !exists {
		logrus.WithFields(e.LogFields()).Trace("Creating new session")
		sess = &session{
			pid:         tlsEvent.PID,
			comm:        tlsEvent.CommBytes,
			sslContext:  tlsEvent.SSLContext,
			request:     &httpRequest{},
			requestBuf:  &bytes.Buffer{},
			response:    &httpResponse{},
			responseBuf: &bytes.Buffer{},
		}
		s.sessions[tlsEvent.SSLContext] = sess
	} else {
		logrus.WithFields(e.LogFields()).Trace("Using existing session")
	}

	// Append data based on direction and parse
	data := tlsEvent.Buffer()
	switch tlsEvent.EventType {
	case event.EventTypeTlsPayloadSend:
		// Client -> Server (Request)
		sess.requestBuf.Write(data)
		sess.request = parseHTTPRequest(sess.requestBuf.Bytes())

		// Emit request event if complete and not yet emitted
		if sess.request != nil && sess.request.isComplete && !sess.requestEventEmitted {
			s.emitHttpRequestEvent(sess)
			sess.requestEventEmitted = true
		}
	case event.EventTypeTlsPayloadRecv:
		// Server -> Client (Response)
		sess.responseBuf.Write(data)
		sess.response = parseHTTPResponse(sess.responseBuf.Bytes())

		// Check if this is an SSE response
		if sess.response != nil && sess.response.isSSE {
			logrus.WithFields(sess.logFields()).Trace("SSE response detected")
			sess.isSSE = true
		}

		// For SSE and chunked responses, process incrementally
		if sess.isSSE && sess.response != nil && sess.response.isChunked {
			// Process SSE events from the current response buffer
			s.processHTTPSSEResponse(sess)
		}

		// Emit response event if complete and not yet emitted
		if sess.response != nil && sess.response.isComplete && !sess.responseEventEmitted {
			s.emitHttpResponseEvent(sess)
			sess.responseEventEmitted = true
		}
	}

	// Clean up session when both events have been emitted
	if sess.requestEventEmitted && sess.responseEventEmitted {
		delete(s.sessions, tlsEvent.SSLContext)
	}
}

func (s *SessionManager) ProcessTlsFreeEvent(e event.Event) {
	// We only handle TlsFree events here
	tlsFreeEvent, ok := e.(*event.TlsFreeEvent)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up the session
	delete(s.sessions, tlsFreeEvent.SSLContext)
}

func (s *SessionManager) emitHttpRequestEvent(sess *session) {
	// Build request event
	event := &event.HttpRequestEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpRequest,
			PID:       sess.pid,
			CommBytes: sess.comm,
		},
		SSLContext:     sess.sslContext,
		Method:         sess.request.method,
		Host:           sess.request.host,
		Path:           sess.request.path,
		RequestHeaders: sess.request.headers,
		RequestPayload: sess.request.body,
	}

	logrus.WithFields(event.LogFields()).Trace(fmt.Sprintf("event#%s", event.Type().String()))

	s.eventBus.Publish(event)
}

func (s *SessionManager) emitHttpResponseEvent(sess *session) {
	if !sess.request.isComplete {
		logrus.WithFields(sess.logFields()).Debug("HTTP request is not complete when HTTP response event is emitted. Expect missing data.")
	}

	// Build response event - includes request info for context
	event := &event.HttpResponseEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpResponse,
			PID:       sess.pid,
			CommBytes: sess.comm,
		},
		SSLContext: sess.sslContext,
		HttpRequestEvent: event.HttpRequestEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeHttpRequest,
				PID:       sess.pid,
				CommBytes: sess.comm,
			},
			SSLContext:     sess.sslContext,
			Method:         sess.request.method,
			Host:           sess.request.host,
			Path:           sess.request.path,
			RequestHeaders: sess.request.headers,
			RequestPayload: sess.request.body,
		},
		Code:            sess.response.statusCode,
		IsChunked:       sess.response.isChunked,
		ResponseHeaders: sess.response.headers,
		ResponsePayload: sess.response.body,
	}

	logrus.WithFields(event.LogFields()).Trace(fmt.Sprintf("event#%s", event.Type().String()))

	s.eventBus.Publish(event)
}

func (s *SessionManager) emitSSEEvent(sess *session, eventType string, data []byte) {
	// Build SSE event - include request and response context
	event := &event.SSEEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpSSE,
			PID:       sess.pid,
			CommBytes: sess.comm,
		},
		SSLContext: sess.sslContext,
		HttpRequestEvent: event.HttpRequestEvent{
			EventHeader: event.EventHeader{
				EventType: event.EventTypeHttpRequest,
				PID:       sess.pid,
				CommBytes: sess.comm,
			},
			SSLContext:     sess.sslContext,
			Method:         sess.request.method,
			Host:           sess.request.host,
			Path:           sess.request.path,
			RequestHeaders: sess.request.headers,
			RequestPayload: sess.request.body,
		},
		SSEEventType: eventType,
		Data:         data,
	}

	logrus.WithFields(event.LogFields()).Trace(fmt.Sprintf("event#%s", event.Type().String()))

	s.eventBus.Publish(event)
}

// Close closes the event channel
func (s *SessionManager) Close() {
	s.eventBus.Unsubscribe(event.EventTypeTlsPayloadRecv, s.ProcessTlsEvent)
	s.eventBus.Unsubscribe(event.EventTypeTlsPayloadSend, s.ProcessTlsEvent)
	s.eventBus.Unsubscribe(event.EventTypeTlsFree, s.ProcessTlsFreeEvent)
}

// parseHTTPRequest parses HTTP request data and returns parsed information
func parseHTTPRequest(data []byte) *httpRequest {
	req := &httpRequest{
		headers: make(map[string]string),
	}

	// Find end of headers
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd == -1 {
		return req // Not complete
	}

	// Parse first line
	firstLineEnd := bytes.Index(data, []byte("\r\n"))
	if firstLineEnd == -1 {
		return req // Not complete
	}

	firstLine := string(data[:firstLineEnd])
	parts := strings.Split(firstLine, " ")

	// Request line: METHOD PATH HTTP/VERSION
	if len(parts) < 3 {
		return req
	}
	req.method = parts[0]
	req.path = parts[1]

	// Parse headers
	hasContentLength := false
	contentLength := 0

	// Handle case where there are no headers (empty header section)
	if headerEnd > firstLineEnd+2 {
		headerLines := string(data[firstLineEnd+2 : headerEnd])
		for _, line := range strings.Split(headerLines, "\r\n") {
			colonIdx := strings.Index(line, ":")
			if colonIdx > 0 {
				key := strings.TrimSpace(line[:colonIdx])
				value := strings.TrimSpace(line[colonIdx+1:])
				req.headers[key] = value

				lowerKey := strings.ToLower(key)
				switch lowerKey {
				case "host":
					req.host = value
				case "content-length":
					hasContentLength = true
					fmt.Sscanf(value, "%d", &contentLength)
				}
			}
		}
	}

	// Check body completeness
	bodyStart := headerEnd + 4

	if hasContentLength {
		// Has Content-Length header
		if bodyStart >= len(data) && contentLength == 0 {
			// No body expected and headers are complete
			req.isComplete = true
		} else if bodyStart < len(data) {
			bodyLength := len(data) - bodyStart
			if bodyLength >= contentLength {
				if contentLength > 0 {
					req.body = data[bodyStart : bodyStart+contentLength]
				}
				req.isComplete = true
			}
		}
	} else {
		// No Content-Length - assume no body for requests
		req.isComplete = true
		if bodyStart < len(data) {
			// But if there is data, include it
			req.body = data[bodyStart:]
		}
	}

	return req
}

// parseHTTPResponse parses HTTP response data and returns parsed information
func parseHTTPResponse(data []byte) *httpResponse {
	resp := &httpResponse{
		headers: make(map[string]string),
	}

	// Find end of headers
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd == -1 {
		return resp // Not complete
	}

	// Parse first line
	firstLineEnd := bytes.Index(data, []byte("\r\n"))
	if firstLineEnd == -1 {
		return resp // Not complete
	}

	firstLine := string(data[:firstLineEnd])
	parts := strings.Split(firstLine, " ")

	// Response line: HTTP/VERSION CODE REASON
	if len(parts) < 2 {
		return resp
	}
	fmt.Sscanf(parts[1], "%d", &resp.statusCode)

	// Parse headers
	hasContentLength := false
	contentLength := 0

	// Handle case where there are no headers (empty header section)
	if headerEnd > firstLineEnd+2 {
		headerLines := string(data[firstLineEnd+2 : headerEnd])
		for _, line := range strings.Split(headerLines, "\r\n") {
			colonIdx := strings.Index(line, ":")
			if colonIdx > 0 {
				key := strings.TrimSpace(line[:colonIdx])
				value := strings.TrimSpace(line[colonIdx+1:])
				resp.headers[key] = value

				lowerKey := strings.ToLower(key)
				if lowerKey == "transfer-encoding" && strings.Contains(strings.ToLower(value), "chunked") {
					resp.isChunked = true
				} else if lowerKey == "content-type" && strings.Contains(strings.ToLower(value), "text/event-stream") {
					resp.isSSE = true
				} else if lowerKey == "content-length" {
					hasContentLength = true
					fmt.Sscanf(value, "%d", &contentLength)
				}
			}
		}
	}

	// Check body completeness
	bodyStart := headerEnd + 4

	if resp.isChunked {
		// For chunked encoding, parse and check completeness
		if bodyStart < len(data) {
			if body, complete := parseChunkedBody(data[bodyStart:]); complete {
				resp.body = body
				resp.isComplete = true
			}
		}
	} else if hasContentLength {
		// Has Content-Length header
		if bodyStart >= len(data) && contentLength == 0 {
			// No body expected and headers are complete
			resp.isComplete = true
		} else if bodyStart < len(data) {
			bodyLength := len(data) - bodyStart
			if bodyLength >= contentLength {
				if contentLength > 0 {
					resp.body = data[bodyStart : bodyStart+contentLength]
				}
				resp.isComplete = true
			}
		}
	} else {
		// No Content-Length and not chunked
		// For responses without Content-Length, assume all data after headers is the body
		// This is common for HTTP/1.0 or when connection will be closed after response
		resp.isComplete = true
		if bodyStart < len(data) {
			resp.body = data[bodyStart:]
		}
	}

	return resp
}

// parseChunkedBody attempts to parse chunked body data
// Returns the parsed body and whether the chunked data is complete
func parseChunkedBody(data []byte) (body []byte, isComplete bool) {
	var result bytes.Buffer
	pos := 0

	for pos < len(data) {
		// Find chunk size line
		lineEnd := bytes.Index(data[pos:], []byte("\r\n"))
		if lineEnd == -1 {
			return result.Bytes(), false // Incomplete - no chunk size line
		}

		// Parse chunk size (in hex)
		sizeStr := string(data[pos : pos+lineEnd])
		var chunkSize int64
		fmt.Sscanf(sizeStr, "%x", &chunkSize)

		// Move past size line
		pos += lineEnd + 2

		// If chunk size is 0, we've reached the end
		if chunkSize == 0 {
			return result.Bytes(), true
		}

		// Check if we have enough data for this chunk
		if pos+int(chunkSize)+2 > len(data) {
			return result.Bytes(), false // Incomplete - not enough data for chunk
		}

		// Append chunk data to result
		result.Write(data[pos : pos+int(chunkSize)])

		// Move past chunk data
		pos += int(chunkSize)

		// Skip trailing CRLF if present
		if pos+1 < len(data) && data[pos] == '\r' && data[pos+1] == '\n' {
			pos += 2
		}
	}

	// Incomplete - no terminating chunk
	// Still return the data we have so far
	return result.Bytes(), false
}

// processHTTPSSEResponse processes SSE events from chunked data incrementally
func (s *SessionManager) processHTTPSSEResponse(sess *session) {
	if !sess.request.isComplete {
		logrus.WithFields(sess.logFields()).Debug("HTTP request is not complete when SSE chunks are processed. Expect missing data.")
	}

	rawData := sess.responseBuf.Bytes()

	// Parse the chunked body from the raw HTTP response data
	// First, find where the headers end
	headerEnd := bytes.Index(rawData, []byte("\r\n\r\n"))
	if headerEnd == -1 {
		return // Headers not complete yet
	}

	// Extract the body portion (after headers)
	bodyStart := headerEnd + 4
	if bodyStart >= len(rawData) {
		return // No body data yet
	}

	bodyData := rawData[bodyStart:]

	// Parse chunks and extract the actual data
	// We ignore the error here,
	// as we want to extract SSE events as soon as they arrive.
	chunkData, _ := parseChunkedBody(bodyData)
	if len(chunkData) == 0 {
		return // No chunk data yet
	}

	// Parse all SSE events from the accumulated chunk data
	allEvents := parseSSEEvents(chunkData)

	// Only process events we haven't sent yet
	if len(allEvents) > sess.sseEventsSent {
		// Send only the new events
		newEvents := allEvents[sess.sseEventsSent:]

		for _, eventData := range newEvents {
			// Extract event type and data content for the SSE event
			eventType, dataContent := extractSSEEventData(eventData)
			if dataContent != nil {
				// Create SSE event with HTTP context
				s.emitSSEEvent(sess, eventType, dataContent)
			}

			sess.sseEventsSent++
		}
	}
}

// parseSSEEvents receives raw response payload (after trimming the chunked parts)
// and returns list of SSE events as raw data.
// Each event contains all fields (data:, event:, id:, retry:, etc.) concatenated.
func parseSSEEvents(data []byte) [][]byte {
	var events [][]byte

	// SSE events are separated by double newlines (\n\n)
	// Each event consists of lines starting with "data:", "event:", "id:", etc.

	lines := bytes.Split(data, []byte("\n"))
	var currentEventLines [][]byte

	for _, line := range lines {
		// Trim any trailing \r (for CRLF line endings)
		line = bytes.TrimSuffix(line, []byte("\r"))

		// Empty line signals end of an event
		if len(line) == 0 {
			if len(currentEventLines) > 0 {
				// Join all lines for this event with newlines
				eventData := bytes.Join(currentEventLines, []byte("\n"))
				events = append(events, eventData)
				currentEventLines = nil
			}
			continue
		}

		// Check if this is a valid SSE field line (contains a colon but not a comment)
		if bytes.Contains(line, []byte(":")) && !bytes.HasPrefix(line, []byte(":")) {
			currentEventLines = append(currentEventLines, line)
		}
		// Lines without colon or comments (starting with :) are ignored
	}

	// Handle any remaining data that wasn't terminated by an empty line
	if len(currentEventLines) > 0 {
		eventData := bytes.Join(currentEventLines, []byte("\n"))
		events = append(events, eventData)
	}

	return events
}

// extractSSEEventData extracts both the event type and data content from a complete SSE event.
// Returns the event type (defaulting to "message" if not specified) and the data content.
func extractSSEEventData(event []byte) (eventType string, data []byte) {
	lines := bytes.Split(event, []byte("\n"))
	var dataLines [][]byte

	dataPrefix := []byte("data:")
	eventPrefix := []byte("event:")
	eventType = "message" // Default per SSE spec

	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))

		if bytes.HasPrefix(line, dataPrefix) {
			dataContent := bytes.TrimSpace(bytes.TrimPrefix(line, dataPrefix))
			dataLines = append(dataLines, dataContent)
		} else if bytes.HasPrefix(line, eventPrefix) {
			extractedType := bytes.TrimSpace(bytes.TrimPrefix(line, eventPrefix))
			if len(extractedType) > 0 {
				eventType = string(extractedType)
			}
		}
	}

	if len(dataLines) == 0 {
		return eventType, nil
	}

	return eventType, bytes.Join(dataLines, []byte("\n"))
}
