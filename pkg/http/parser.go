package http

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Parser handles HTTP message parsing
type Parser struct{}

// NewParser creates a new HTTP parser
func NewParser() *Parser {
	return &Parser{}
}

// HTTPMessage represents a parsed HTTP message
type HTTPMessage struct {
	IsRequest     bool
	Method        string // For requests
	Path          string // For requests
	StatusCode    int    // For responses
	Headers       map[string]string
	Body          []byte
	ContentLength int
	IsChunked     bool
	RawMessage    []byte
}

// ParseMessage attempts to parse an HTTP message and extract the body
func (p *Parser) ParseMessage(data []byte) (*HTTPMessage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	msg := &HTTPMessage{
		Headers:    make(map[string]string),
		RawMessage: data,
	}

	// Split headers and body
	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd == -1 {
		// Try with just \n\n for compatibility
		headerEnd = bytes.Index(data, []byte("\n\n"))
		if headerEnd == -1 {
			return nil, fmt.Errorf("no header/body separator found")
		}
	}

	headerSection := data[:headerEnd]
	bodyStart := headerEnd + 4 // Skip \r\n\r\n
	if bytes.Index(data, []byte("\r\n\r\n")) == -1 {
		bodyStart = headerEnd + 2 // Skip \n\n
	}

	// Parse first line
	lines := strings.Split(string(headerSection), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("no header lines found")
	}

	firstLine := strings.TrimSpace(lines[0])
	if err := p.parseFirstLine(firstLine, msg); err != nil {
		return nil, fmt.Errorf("failed to parse first line: %w", err)
	}

	// Parse headers
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])
		msg.Headers[strings.ToLower(key)] = value
	}

	// Check Content-Length
	if cl, exists := msg.Headers["content-length"]; exists {
		if length, err := strconv.Atoi(cl); err == nil {
			msg.ContentLength = length
		}
	}

	// Check for chunked encoding
	if te, exists := msg.Headers["transfer-encoding"]; exists {
		msg.IsChunked = strings.Contains(strings.ToLower(te), "chunked")
	}

	// Extract body
	if bodyStart < len(data) {
		if msg.IsChunked {
			// Simple chunked body parsing (HTTP/1.1)
			body, err := p.parseChunkedBody(data[bodyStart:])
			if err != nil {
				// If chunked parsing fails, just use raw body
				msg.Body = data[bodyStart:]
			} else {
				msg.Body = body
			}
		} else if msg.ContentLength > 0 {
			// Use Content-Length
			bodyEnd := bodyStart + msg.ContentLength
			if bodyEnd > len(data) {
				bodyEnd = len(data)
			}
			msg.Body = data[bodyStart:bodyEnd]
		} else {
			// No Content-Length, use all remaining data
			msg.Body = data[bodyStart:]
		}
	}

	return msg, nil
}

// parseFirstLine parses the HTTP request/response first line
func (p *Parser) parseFirstLine(line string, msg *HTTPMessage) error {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return fmt.Errorf("invalid first line format")
	}

	// Check if it's a request or response
	if strings.HasPrefix(parts[0], "HTTP/") {
		// Response: HTTP/1.1 200 OK
		msg.IsRequest = false
		if code, err := strconv.Atoi(parts[1]); err == nil {
			msg.StatusCode = code
		}
	} else {
		// Request: GET /path HTTP/1.1
		msg.IsRequest = true
		msg.Method = parts[0]
		msg.Path = parts[1]
	}

	return nil
}

// parseChunkedBody parses a chunked HTTP body (simplified version)
func (p *Parser) parseChunkedBody(data []byte) ([]byte, error) {
	var result []byte
	offset := 0

	for offset < len(data) {
		// Find chunk size line
		lineEnd := bytes.Index(data[offset:], []byte("\r\n"))
		if lineEnd == -1 {
			break
		}

		// Parse chunk size (hexadecimal)
		sizeLine := string(data[offset : offset+lineEnd])
		chunkSize, err := strconv.ParseInt(sizeLine, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid chunk size: %w", err)
		}

		// If chunk size is 0, we're done
		if chunkSize == 0 {
			break
		}

		// Move to chunk data
		offset += lineEnd + 2 // Skip \r\n

		// Read chunk data
		if offset+int(chunkSize) > len(data) {
			break
		}

		result = append(result, data[offset:offset+int(chunkSize)]...)
		offset += int(chunkSize) + 2 // Skip chunk data and trailing \r\n
	}

	return result, nil
}

// ExtractJSON attempts to extract JSON body from HTTP message
// Returns the JSON body if found, or nil if not a JSON message
func (p *Parser) ExtractJSON(data []byte) []byte {
	msg, err := p.ParseMessage(data)
	if err != nil {
		return nil
	}

	// Check Content-Type header
	if ct, exists := msg.Headers["content-type"]; exists {
		if !strings.Contains(strings.ToLower(ct), "application/json") {
			return nil
		}
	}

	// Check if body looks like JSON
	body := bytes.TrimSpace(msg.Body)
	if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
		return body
	}

	return nil
}
