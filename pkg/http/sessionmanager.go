package http

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

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

	request    *httpRequest
	requestBuf *bytes.Buffer

	response    *httpResponse
	responseBuf *bytes.Buffer

	// Event emission tracking
	requestEventEmitted  bool
	responseEventEmitted bool

	// SSE tracking
	isSSE         bool
	sseBuffer     *bytes.Buffer
	sseEventsSent int // Track how many SSE events we've already sent
}

type SessionManager struct {
	mu sync.Mutex

	sessions    map[uint64]*session // key is SSL context
	httpEventCh chan event.Event
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:    make(map[uint64]*session),
		httpEventCh: make(chan event.Event, 100),
	}
}

func (s *SessionManager) ProcessTlsEvent(e *event.TlsPayloadEvent) error {
	// Only process HTTP/1.1 events for now.
	if e.HttpVersion != event.HttpVersion1 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Get or create session
	sess, exists := s.sessions[e.SSLContext]
	if !exists {
		sess = &session{
			sslContext:  e.SSLContext,
			requestBuf:  &bytes.Buffer{},
			responseBuf: &bytes.Buffer{},
			sseBuffer:   &bytes.Buffer{},
		}
		s.sessions[e.SSLContext] = sess
	}

	// Append data based on direction and parse
	data := e.Buffer()
	switch e.EventType {
	case event.EventTypeTlsPayloadSend:
		// Client -> Server (Request)
		sess.requestBuf.Write(data)
		sess.request = parseHTTPRequest(sess.requestBuf.Bytes())
	case event.EventTypeTlsPayloadRecv:
		// Server -> Client (Response)
		sess.responseBuf.Write(data)
		sess.response = parseHTTPResponse(sess.responseBuf.Bytes())

		// Check if this is an SSE response
		if sess.response != nil && sess.response.isSSE {
			logrus.Tracef("SSE response detected")
			sess.isSSE = true
		}

		// For SSE or chunked responses, process incrementally
		if sess.isSSE && sess.response != nil && sess.response.isChunked {
			// Process SSE events from the current response buffer
			s.processSSEChunks(sess, sess.responseBuf.Bytes())
		}
	}

	// Emit request event if complete and not yet emitted
	if sess.request != nil && sess.request.isComplete && !sess.requestEventEmitted {
		s.emitHttpRequestEvent(sess, e)
		sess.requestEventEmitted = true
	}

	// Emit response event if complete and not yet emitted
	if sess.response != nil && sess.response.isComplete && !sess.responseEventEmitted {
		s.emitHttpResponseEvent(sess, e)
		sess.responseEventEmitted = true
	}

	// Clean up session when both events have been emitted
	if sess.requestEventEmitted && sess.responseEventEmitted {
		delete(s.sessions, e.SSLContext)
	}

	return nil
}

func (s *SessionManager) ProcessTlsFreeEvent(e *event.TlsFreeEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up the session
	delete(s.sessions, e.SSLContext)

	return nil
}

func (s *SessionManager) emitHttpRequestEvent(sess *session, e *event.TlsPayloadEvent) {
	// Build request event
	event := event.HttpRequestEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpRequest,
			PID:       e.PID,
			CommBytes: e.CommBytes,
		},
		SSLContext:     sess.sslContext,
		Method:         sess.request.method,
		Host:           sess.request.host,
		Path:           sess.request.path,
		RequestHeaders: sess.request.headers,
		RequestPayload: sess.request.body,
	}

	select {
	case s.httpEventCh <- &event:
	default:
		logrus.Warn("HTTP event channel is full, dropping HTTP request event")
	}
}

func (s *SessionManager) emitHttpResponseEvent(sess *session, e *event.TlsPayloadEvent) {
	// Build response event - includes request info for context
	event := event.HttpResponseEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpResponse,
			PID:       e.PID,
			CommBytes: e.CommBytes,
		},
		SSLContext:      sess.sslContext,
		Method:          sess.request.method,
		Host:            sess.request.host,
		Path:            sess.request.path,
		Code:            sess.response.statusCode,
		IsChunked:       sess.response.isChunked,
		ResponseHeaders: sess.response.headers,
		ResponsePayload: sess.response.body,
	}

	select {
	case s.httpEventCh <- &event:
	default:
		logrus.Warn("HTTP event channel is full, dropping HTTP response event")
	}
}

func (s *SessionManager) emitSSEEvent(sess *session, data []byte) {
	// Build SSE event - include request and response context
	event := event.SSEEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpSSE,
		},
		SSLContext: sess.sslContext,
		Method:     sess.request.method,
		Host:       sess.request.host,
		Path:       sess.request.path,
		Code:       sess.response.statusCode,
		IsChunked:  sess.response.isChunked,
		Data:       data,
	}

	select {
	case s.httpEventCh <- &event:
	default:
		logrus.Warn("HTTP event channel is full, dropping HTTP SSE event")
	}
}

// HTTPEvents returns a channel for receiving HTTP events
func (s *SessionManager) HTTPEvents() <-chan event.Event {
	return s.httpEventCh
}

// Close closes the event channels
func (s *SessionManager) Close() {
	close(s.httpEventCh)
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
				if lowerKey == "host" {
					req.host = value
				} else if lowerKey == "content-length" {
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
// If partial is true, it will attempt to parse incomplete chunked responses
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
// If partial is true, it will return whatever chunks are available without requiring the terminating zero chunk
func parseChunkedBody(data []byte) (body []byte, isComplete bool) {
	var result bytes.Buffer
	pos := 0

	for pos < len(data) {
		// Find chunk size line
		lineEnd := bytes.Index(data[pos:], []byte("\r\n"))
		if lineEnd == -1 {
			return nil, false // Incomplete - no chunk size line
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
			return nil, false // Incomplete - not enough data for chunk
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

// processSSEChunks processes SSE events from chunked data incrementally
func (s *SessionManager) processSSEChunks(sess *session, rawData []byte) {

	// Only process if we have a request and SSE response
	if sess.request == nil || !sess.request.isComplete || sess.response == nil {
		return
	}

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
	chunkData, _ := parseChunkedBody(bodyData)
	if len(chunkData) == 0 {
		return // No chunk data yet
	}

	// Parse all SSE events from the accumulated chunk data
	// This extracts just the data portion from each SSE event
	allEvents := parseSSEEvents(chunkData)

	// Only process events we haven't sent yet
	if len(allEvents) > sess.sseEventsSent {
		// Send only the new events
		newEvents := allEvents[sess.sseEventsSent:]

		for _, eventData := range newEvents {
			// Create SSE event with HTTP context
			s.emitSSEEvent(sess, eventData)

			sess.sseEventsSent++
		}
	}
}

// parseSSEEvents parses SSE events from raw data
// Returns a slice of SSE event data (just the data portion, not the full event text)
func parseSSEEvents(data []byte) [][]byte {
	var events [][]byte

	// SSE events are separated by double newlines (\n\n)
	// Each event consists of lines starting with "data:", "event:", "id:", etc.

	var currentEventData bytes.Buffer
	hasData := false
	pos := 0

	for pos < len(data) {
		// Find the next newline
		nextNewline := bytes.IndexByte(data[pos:], '\n')
		if nextNewline == -1 {
			// No more newlines, add remaining data to current event if any
			if pos < len(data) {
				line := data[pos:]
				if bytes.HasPrefix(line, []byte("data:")) {
					// Extract the data after "data:" and any whitespace
					dataContent := bytes.TrimSpace(line[5:])
					if currentEventData.Len() > 0 {
						currentEventData.WriteByte('\n')
					}
					currentEventData.Write(dataContent)
					hasData = true
				}
			}
			break
		}

		// Extract the line (excluding the newline)
		line := data[pos : pos+nextNewline]
		pos = pos + nextNewline + 1

		// Trim any trailing \r
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		// Check if this is an empty line (event separator)
		if len(line) == 0 {
			// If we have accumulated data, this marks the end of an event
			if hasData {
				eventData := make([]byte, currentEventData.Len())
				copy(eventData, currentEventData.Bytes())
				events = append(events, eventData)
				currentEventData.Reset()
				hasData = false
			}
			continue
		}

		// Check if this is a data line
		if bytes.HasPrefix(line, []byte("data:")) {
			// Extract the data after "data:" and any whitespace
			dataContent := bytes.TrimSpace(line[5:])
			if currentEventData.Len() > 0 {
				currentEventData.WriteByte('\n')
			}
			currentEventData.Write(dataContent)
			hasData = true
		}
		// For now, we ignore other SSE fields (event:, id:, retry:, etc.)
	}

	// If we have remaining content at the end, it's an incomplete event
	// Don't add it to events

	return events
}
