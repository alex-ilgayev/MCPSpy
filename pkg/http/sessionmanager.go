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
}

// session tracks HTTP communication for a single SSL context
type session struct {
	sslContext uint64

	request    *httpRequest
	requestBuf *bytes.Buffer

	response    *httpResponse
	responseBuf *bytes.Buffer
}

type SessionManager struct {
	mu sync.Mutex

	sessions map[uint64]*session // key is SSL context
	eventCh  chan event.HttpEvent
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[uint64]*session),
		eventCh:  make(chan event.HttpEvent, 100),
	}
}

func (s *SessionManager) ProcessTlsEvent(e *event.TlsEvent) error {
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
		}
		s.sessions[e.SSLContext] = sess
	}

	// Append data based on direction and parse
	data := e.Buffer()
	switch e.EventType {
	case event.EventTypeTlsSend:
		// Client -> Server (Request)
		sess.requestBuf.Write(data)
		sess.request = parseHTTPRequest(sess.requestBuf.Bytes())
	case event.EventTypeTlsRecv:
		// Server -> Client (Response)
		sess.responseBuf.Write(data)
		sess.response = parseHTTPResponse(sess.responseBuf.Bytes())
	}

	// If we have both complete request and response, emit event
	if sess.request != nil && sess.request.isComplete &&
		sess.response != nil && sess.response.isComplete {
		// Build event from parsed data
		event := event.HttpEvent{
			SSLContext:      sess.sslContext,
			Method:          sess.request.method,
			Host:            sess.request.host,
			Path:            sess.request.path,
			Code:            sess.response.statusCode,
			IsChunked:       sess.response.isChunked,
			RequestHeaders:  sess.request.headers,
			ResponseHeaders: sess.response.headers,
			RequestPayload:  sess.request.body,
			ResponsePayload: sess.response.body,
		}

		select {
		case s.eventCh <- event:
		default:
			logrus.Warn("HTTP event channel is full, dropping event")
		}

		// Clean up session
		delete(s.sessions, e.SSLContext)
	}

	return nil
}

// Events returns a channel for receiving events
func (s *SessionManager) HTTPEvents() <-chan event.HttpEvent {
	return s.eventCh
}

// Close closes the event channel
func (s *SessionManager) Close() {
	close(s.eventCh)
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
	headerLines := string(data[firstLineEnd+2 : headerEnd])
	hasContentLength := false
	contentLength := 0

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
	headerLines := string(data[firstLineEnd+2 : headerEnd])
	hasContentLength := false
	contentLength := 0

	for _, line := range strings.Split(headerLines, "\r\n") {
		colonIdx := strings.Index(line, ":")
		if colonIdx > 0 {
			key := strings.TrimSpace(line[:colonIdx])
			value := strings.TrimSpace(line[colonIdx+1:])
			resp.headers[key] = value

			lowerKey := strings.ToLower(key)
			if lowerKey == "transfer-encoding" && strings.Contains(strings.ToLower(value), "chunked") {
				resp.isChunked = true
			} else if lowerKey == "content-length" {
				hasContentLength = true
				fmt.Sscanf(value, "%d", &contentLength)
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

		// Move past chunk data and trailing CRLF
		pos += int(chunkSize) + 2
	}

	return nil, false // Incomplete - no terminating chunk
}
