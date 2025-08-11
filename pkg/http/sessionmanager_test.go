package http

import (
	"testing"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// containsBytes is a helper function to check if a slice contains a subsequence
func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		found := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

func TestSessionManager_BasicRequestResponse(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(12345)

	// Simulate HTTP request
	requestData := []byte("GET /api/test HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)

	// Process request
	err := sm.ProcessTlsEvent(requestEvent)
	if err != nil {
		t.Fatalf("Failed to process request: %v", err)
	}

	// Simulate HTTP response
	responseData := []byte("HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\nHello, World!")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)

	// Process response
	err = sm.ProcessTlsEvent(responseEvent)
	if err != nil {
		t.Fatalf("Failed to process response: %v", err)
	}

	// Check if request event was emitted
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.SSLContext != sslCtx {
			t.Errorf("Expected SSL context %d, got %d", sslCtx, httpEvent.SSLContext)
		}
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
		if httpEvent.Path != "/api/test" {
			t.Errorf("Expected path /api/test, got %s", httpEvent.Path)
		}
		if httpEvent.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", httpEvent.Host)
		}
		if len(httpEvent.RequestPayload) != 0 {
			t.Errorf("Expected empty request payload, got %q", httpEvent.RequestPayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Check if response event was emitted
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if httpEvent.SSLContext != sslCtx {
			t.Errorf("Expected SSL context %d, got %d", sslCtx, httpEvent.SSLContext)
		}
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
		if httpEvent.Path != "/api/test" {
			t.Errorf("Expected path /api/test, got %s", httpEvent.Path)
		}
		if httpEvent.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", httpEvent.Host)
		}
		if httpEvent.Code != 200 {
			t.Errorf("Expected status code 200, got %d", httpEvent.Code)
		}
		if string(httpEvent.ResponsePayload) != "Hello, World!" {
			t.Errorf("Expected response payload 'Hello, World!', got %q", httpEvent.ResponsePayload)
		}
		if httpEvent.ResponseHeaders["Content-Length"] != "13" {
			t.Errorf("Expected Content-Length header '13', got %s", httpEvent.ResponseHeaders["Content-Length"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No response event received")
	}
}

func TestSessionManager_FragmentedPayload(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(67890)

	// Send request in fragments
	requestPart1 := []byte("GET /api/test HTTP/1.1\r\nHost: ")
	requestPart2 := []byte("example.com\r\nContent-Length: 0\r\n\r\n")

	// First fragment
	event1 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestPart1)),
	}
	copy(event1.Buf[:], requestPart1)
	sm.ProcessTlsEvent(event1)

	// Second fragment
	event2 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestPart2)),
	}
	copy(event2.Buf[:], requestPart2)
	sm.ProcessTlsEvent(event2)

	// Send complete response
	responseData := []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Check request event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
		if httpEvent.Path != "/api/test" {
			t.Errorf("Expected path /api/test, got %s", httpEvent.Path)
		}
		if httpEvent.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", httpEvent.Host)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Check response event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if httpEvent.Code != 200 {
			t.Errorf("Expected status code 200, got %d", httpEvent.Code)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No response event received")
	}
}

func TestSessionManager_MultipleSessions(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx1 := uint64(111)
	sslCtx2 := uint64(222)

	// Session 1 request
	req1 := []byte("GET /session1 HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n")
	event1 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx1,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(req1)),
	}
	copy(event1.Buf[:], req1)
	sm.ProcessTlsEvent(event1)

	// Session 2 request
	req2 := []byte("GET /session2 HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n")
	event2 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx2,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(req2)),
	}
	copy(event2.Buf[:], req2)
	sm.ProcessTlsEvent(event2)

	// Session 2 response (completes session 2 first)
	resp2 := []byte("HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\nsession2")
	event3 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx2,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(resp2)),
	}
	copy(event3.Buf[:], resp2)
	sm.ProcessTlsEvent(event3)

	// Session 1 response
	resp1 := []byte("HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\nsession1")
	event4 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx1,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(resp1)),
	}
	copy(event4.Buf[:], resp1)
	sm.ProcessTlsEvent(event4)

	// Verify all events are received (2 requests + 2 responses = 4 events)
	requestEvents := make(map[uint64]*event.HttpRequestEvent)
	responseEvents := make(map[uint64]*event.HttpResponseEvent)

	for i := 0; i < 4; i++ {
		select {
		case evt := <-sm.HTTPEvents():
			if evt.Type() == event.EventTypeHttpRequest {
				requestEvents[evt.(*event.HttpRequestEvent).SSLContext] = evt.(*event.HttpRequestEvent)
			} else if evt.Type() == event.EventTypeHttpResponse {
				responseEvents[evt.(*event.HttpResponseEvent).SSLContext] = evt.(*event.HttpResponseEvent)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected 4 events, timeout waiting")
		}
	}

	// Verify session 1 request
	if event1, ok := requestEvents[sslCtx1]; !ok {
		t.Error("Did not receive request event for session 1")
	} else {
		if event1.Path != "/session1" {
			t.Errorf("Session 1 request: expected path /session1, got %s", event1.Path)
		}
	}

	// Verify session 1 response
	if event1, ok := responseEvents[sslCtx1]; !ok {
		t.Error("Did not receive response event for session 1")
	} else {
		if event1.Path != "/session1" {
			t.Errorf("Session 1 response: expected path /session1, got %s", event1.Path)
		}
		if string(event1.ResponsePayload) != "session1" {
			t.Errorf("Session 1: expected response payload 'session1', got %q", event1.ResponsePayload)
		}
	}

	// Verify session 2 request
	if event2, ok := requestEvents[sslCtx2]; !ok {
		t.Error("Did not receive request event for session 2")
	} else {
		if event2.Path != "/session2" {
			t.Errorf("Session 2 request: expected path /session2, got %s", event2.Path)
		}
	}

	// Verify session 2 response
	if event2, ok := responseEvents[sslCtx2]; !ok {
		t.Error("Did not receive response event for session 2")
	} else {
		if event2.Path != "/session2" {
			t.Errorf("Session 2 response: expected path /session2, got %s", event2.Path)
		}
		if string(event2.ResponsePayload) != "session2" {
			t.Errorf("Session 2: expected response payload 'session2', got %q", event2.ResponsePayload)
		}
	}
}

func TestSessionManager_IgnoresNonHTTP11(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// HTTP/2 event should be ignored
	event := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  uint64(999),
		HttpVersion: event.HttpVersion2,
		BufSize:     10,
	}
	copy(event.Buf[:], []byte("some data"))

	err := sm.ProcessTlsEvent(event)
	if err != nil {
		t.Fatalf("ProcessTlsEvent should not fail for non-HTTP/1.1: %v", err)
	}

	// Verify no event is emitted
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not emit event for non-HTTP/1.1")
	case <-time.After(50 * time.Millisecond):
		// Expected: no event
	}
}

func TestParseHTTPMessage_Completeness(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		isRequest bool
		complete  bool
	}{
		{
			name:      "incomplete headers",
			data:      []byte("GET / HTTP/1.1\r\nHost: example.com"),
			isRequest: true,
			complete:  false,
		},
		{
			name:      "complete headers no body",
			data:      []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"),
			isRequest: true,
			complete:  true,
		},
		{
			name:      "headers with content-length and complete body",
			data:      []byte("POST / HTTP/1.1\r\nContent-Length: 5\r\n\r\nhello"),
			isRequest: true,
			complete:  true,
		},
		{
			name:      "headers with content-length but incomplete body",
			data:      []byte("POST / HTTP/1.1\r\nContent-Length: 10\r\n\r\nhello"),
			isRequest: true,
			complete:  false,
		},
		{
			name:      "response with body",
			data:      []byte("HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\nHello, World!"),
			isRequest: false,
			complete:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var isComplete bool
			if tt.isRequest {
				req := parseHTTPRequest(tt.data)
				isComplete = req.isComplete
			} else {
				resp := parseHTTPResponse(tt.data)
				isComplete = resp.isComplete
			}
			if isComplete != tt.complete {
				t.Errorf("Expected isComplete=%v, got %v for data: %q", tt.complete, isComplete, tt.data)
			}
		})
	}
}

func TestParseHTTPRequest(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		wantMethod   string
		wantPath     string
		wantHost     string
		wantBody     string
		wantComplete bool
	}{
		{
			name:         "simple GET request",
			data:         []byte("GET /api/test HTTP/1.1\r\nHost: example.com\r\n\r\n"),
			wantMethod:   "GET",
			wantPath:     "/api/test",
			wantHost:     "example.com",
			wantComplete: true,
		},
		{
			name:         "POST request with body",
			data:         []byte("POST /submit HTTP/1.1\r\nHost: api.test.com\r\nContent-Length: 4\r\n\r\ndata"),
			wantMethod:   "POST",
			wantPath:     "/submit",
			wantHost:     "api.test.com",
			wantBody:     "data",
			wantComplete: true,
		},
		{
			name:         "request with port in host",
			data:         []byte("GET / HTTP/1.1\r\nHost: localhost:8080\r\n\r\n"),
			wantMethod:   "GET",
			wantPath:     "/",
			wantHost:     "localhost:8080",
			wantComplete: true,
		},
		{
			name:         "invalid request - no headers end",
			data:         []byte("GET / HTTP/1.1\r\nHost: example.com"),
			wantComplete: false,
		},
		{
			name:         "invalid request line",
			data:         []byte("INVALID REQUEST\r\n\r\n"),
			wantComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := parseHTTPRequest(tt.data)

			if req.isComplete != tt.wantComplete {
				t.Errorf("isComplete = %v, want %v", req.isComplete, tt.wantComplete)
			}

			if req.isComplete {
				if req.method != tt.wantMethod {
					t.Errorf("method = %q, want %q", req.method, tt.wantMethod)
				}
				if req.path != tt.wantPath {
					t.Errorf("path = %q, want %q", req.path, tt.wantPath)
				}
				if req.host != tt.wantHost {
					t.Errorf("host = %q, want %q", req.host, tt.wantHost)
				}
				if string(req.body) != tt.wantBody {
					t.Errorf("body = %q, want %q", req.body, tt.wantBody)
				}
			}
		})
	}
}

func TestParseHTTPResponse(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		wantCode     int
		wantHeaders  map[string]string
		wantBody     string
		wantComplete bool
	}{
		{
			name:     "200 OK with body",
			data:     []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 5\r\n\r\nhello"),
			wantCode: 200,
			wantHeaders: map[string]string{
				"Content-Type":   "text/plain",
				"Content-Length": "5",
			},
			wantBody:     "hello",
			wantComplete: true,
		},
		{
			name:     "404 Not Found",
			data:     []byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"),
			wantCode: 404,
			wantHeaders: map[string]string{
				"Content-Length": "0",
			},
			wantBody:     "",
			wantComplete: true,
		},
		{
			name:     "201 Created with JSON",
			data:     []byte("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\n\r\n{\"id\":123}"),
			wantCode: 201,
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			wantBody:     "{\"id\":123}",
			wantComplete: true,
		},
		{
			name:         "invalid response - no headers end",
			data:         []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain"),
			wantComplete: false,
		},
		{
			name:         "invalid response line",
			data:         []byte("INVALID\r\n\r\n"),
			wantComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := parseHTTPResponse(tt.data)

			if resp.isComplete != tt.wantComplete {
				t.Errorf("isComplete = %v, want %v", resp.isComplete, tt.wantComplete)
			}

			if resp.isComplete {
				if resp.statusCode != tt.wantCode {
					t.Errorf("statusCode = %d, want %d", resp.statusCode, tt.wantCode)
				}
				if string(resp.body) != tt.wantBody {
					t.Errorf("body = %q, want %q", resp.body, tt.wantBody)
				}
				for k, v := range tt.wantHeaders {
					if resp.headers[k] != v {
						t.Errorf("header %s = %q, want %q", k, resp.headers[k], v)
					}
				}
			}
		})
	}
}

func TestChunkedTransferEncoding(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(33333)

	// Request
	requestData := []byte("GET /api/chunked HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Chunked response
	responseData := []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Check request event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Check response event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if !httpEvent.IsChunked {
			t.Error("Expected IsChunked to be true")
		}
		if string(httpEvent.ResponsePayload) != "hello world" {
			t.Errorf("Expected aggregated response payload 'hello world', got %q", httpEvent.ResponsePayload)
		}
		if httpEvent.ResponseHeaders["Transfer-Encoding"] != "chunked" {
			t.Errorf("Expected Transfer-Encoding header 'chunked', got %s", httpEvent.ResponseHeaders["Transfer-Encoding"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No response event received")
	}
}

func TestParseChunkedBody_Completeness(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		wantComplete bool
		wantBody     string
	}{
		{
			name:         "complete single chunk",
			data:         []byte("5\r\nhello\r\n0\r\n\r\n"),
			wantComplete: true,
			wantBody:     "hello",
		},
		{
			name:         "complete multiple chunks",
			data:         []byte("5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n"),
			wantComplete: true,
			wantBody:     "hello world",
		},
		{
			name:         "incomplete - missing terminator",
			data:         []byte("5\r\nhello\r\n"),
			wantComplete: false,
			wantBody:     "",
		},
		{
			name:         "incomplete - missing chunk data",
			data:         []byte("5\r\nhel"),
			wantComplete: false,
			wantBody:     "",
		},
		{
			name:         "incomplete - missing final chunk",
			data:         []byte("5\r\nhello\r\n6\r\n world\r\n"),
			wantComplete: false,
			wantBody:     "",
		},
		{
			name:         "empty chunks",
			data:         []byte("0\r\n\r\n"),
			wantComplete: true,
			wantBody:     "",
		},
		{
			name:         "chunk with extensions",
			data:         []byte("5;ext=value\r\nhello\r\n0\r\n\r\n"),
			wantComplete: true,
			wantBody:     "hello",
		},
		{
			name:         "hex chunk sizes",
			data:         []byte("a\r\n0123456789\r\n0\r\n\r\n"),
			wantComplete: true,
			wantBody:     "0123456789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, complete := parseChunkedBody(tt.data)
			if complete != tt.wantComplete {
				t.Errorf("Expected complete=%v, got %v for data: %q", tt.wantComplete, complete, tt.data)
			}
			if complete && string(body) != tt.wantBody {
				t.Errorf("Expected body=%q, got %q", tt.wantBody, body)
			}
		})
	}
}

func TestSessionManager_FragmentedChunkedResponse(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(44444)

	// Request
	requestData := []byte("GET /api/test HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Should emit request event immediately after request is complete
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Response fragment 1 - headers and first chunk
	respPart1 := []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello")
	event1 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(respPart1)),
	}
	copy(event1.Buf[:], respPart1)
	sm.ProcessTlsEvent(event1)

	// Should not emit response event yet (response is incomplete)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not emit response event for incomplete chunked response")
	case <-time.After(50 * time.Millisecond):
		// Expected
	}

	// Response fragment 2 - complete the message
	respPart2 := []byte("\r\n0\r\n\r\n")
	event2 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(respPart2)),
	}
	copy(event2.Buf[:], respPart2)
	sm.ProcessTlsEvent(event2)

	// Now should emit response event (request was already emitted earlier)
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if !httpEvent.IsChunked {
			t.Error("Expected IsChunked to be true")
		}
		if string(httpEvent.ResponsePayload) != "hello" {
			t.Errorf("Expected response payload 'hello', got %q", httpEvent.ResponsePayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No response event received")
	}
}

func TestSessionManager_RequestWithPayload(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(55555)

	// POST request with JSON payload
	requestData := []byte("POST /api/users HTTP/1.1\r\nHost: api.example.com\r\nContent-Type: application/json\r\nContent-Length: 24\r\n\r\n{\"name\":\"John\",\"age\":30}")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Response
	responseData := []byte("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 14\r\n\r\n{\"id\":\"12345\"}")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Check request event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "POST" {
			t.Errorf("Expected method POST, got %s", httpEvent.Method)
		}
		if httpEvent.Path != "/api/users" {
			t.Errorf("Expected path /api/users, got %s", httpEvent.Path)
		}
		if httpEvent.Host != "api.example.com" {
			t.Errorf("Expected host api.example.com, got %s", httpEvent.Host)
		}
		expectedReqPayload := "{\"name\":\"John\",\"age\":30}"
		if string(httpEvent.RequestPayload) != expectedReqPayload {
			t.Errorf("Expected request payload %q, got %q", expectedReqPayload, httpEvent.RequestPayload)
		}
		// Check request headers
		if httpEvent.RequestHeaders["Content-Type"] != "application/json" {
			t.Errorf("Expected request Content-Type 'application/json', got %s", httpEvent.RequestHeaders["Content-Type"])
		}
		if httpEvent.RequestHeaders["Content-Length"] != "24" {
			t.Errorf("Expected request Content-Length '24', got %s", httpEvent.RequestHeaders["Content-Length"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Check response event
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if httpEvent.Code != 201 {
			t.Errorf("Expected status code 201, got %d", httpEvent.Code)
		}
		expectedRespPayload := "{\"id\":\"12345\"}"
		if string(httpEvent.ResponsePayload) != expectedRespPayload {
			t.Errorf("Expected response payload %q, got %q", expectedRespPayload, httpEvent.ResponsePayload)
		}
		// Check response headers
		if httpEvent.ResponseHeaders["Content-Type"] != "application/json" {
			t.Errorf("Expected response Content-Type 'application/json', got %s", httpEvent.ResponseHeaders["Content-Type"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No response event received")
	}
}

func TestProcessTlsFreeEvent_DeletesSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(99999)

	// Create a session with a request
	requestData := []byte("GET /api/test HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       1234,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Verify session exists
	sm.mu.Lock()
	_, exists := sm.sessions[sslCtx]
	sm.mu.Unlock()
	if !exists {
		t.Fatal("Session should exist after processing request")
	}

	// Send TlsFreeEvent
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       1234,
		},
		SSLContext: sslCtx,
	}
	err := sm.ProcessTlsFreeEvent(freeEvent)
	if err != nil {
		t.Fatalf("ProcessTlsFreeEvent failed: %v", err)
	}

	// Verify session is deleted
	sm.mu.Lock()
	_, exists = sm.sessions[sslCtx]
	sm.mu.Unlock()
	if exists {
		t.Fatal("Session should be deleted after TlsFreeEvent")
	}

	// Should receive a request event (request is complete)
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should have received request event")
	}
}

func TestProcessTlsFreeEvent_IncompleteChunkedResponse(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(88888)

	// Send request
	requestData := []byte("GET /stream HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       5678,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Send incomplete chunked response (missing final 0 chunk)
	responseData := []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nHello\r\n6\r\n World\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       5678,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Should receive request event (request is complete)
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Should have received request event")
	}

	// No response event should be emitted yet (incomplete)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive response event for incomplete chunked response")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event
	}

	// Send TlsFreeEvent to force cleanup
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       5678,
		},
		SSLContext: sslCtx,
	}
	sm.ProcessTlsFreeEvent(freeEvent)

	// Session should be deleted but no response event (incomplete response)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive response event for incomplete chunked response even after TlsFree")
	case <-time.After(100 * time.Millisecond):
		// Expected - no response event for incomplete response
	}
}

func TestProcessTlsFreeEvent_NoSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Send TlsFreeEvent for non-existent session
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       1111,
		},
		SSLContext: 66666,
	}

	err := sm.ProcessTlsFreeEvent(freeEvent)
	if err != nil {
		t.Fatalf("ProcessTlsFreeEvent should not fail for non-existent session: %v", err)
	}

	// No event should be emitted
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive event for non-existent session")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event
	}
}

func TestProcessTlsFreeEvent_OnlyRequest(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(55555)

	// Send only request (no response)
	requestData := []byte("POST /api/data HTTP/1.1\r\nHost: api.example.com\r\nContent-Length: 7\r\n\r\n{\"a\":1}")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       2222,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Should receive request event (request is complete)
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "POST" {
			t.Errorf("Expected method POST, got %s", httpEvent.Method)
		}
		if string(httpEvent.RequestPayload) != "{\"a\":1}" {
			t.Errorf("Expected request payload '{\"a\":1}', got %q", httpEvent.RequestPayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should have received request event")
	}

	// Send TlsFreeEvent
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       2222,
		},
		SSLContext: sslCtx,
	}
	sm.ProcessTlsFreeEvent(freeEvent)

	// Should NOT receive response event (no response was sent)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive response event for session with only request")
	case <-time.After(100 * time.Millisecond):
		// Expected - no response event
	}
}

func TestSessionManager_SSEResponse(t *testing.T) {
	// Create session manager without callback (will use channel)
	sm := NewSessionManager()
	defer sm.Close()

	// Track SSE events from channel
	var sseEvents [][]byte
	var sseContexts []*event.SSEEvent

	sslCtx := uint64(77777)

	// Send request
	requestData := []byte("GET /events HTTP/1.1\r\nHost: example.com\r\nAccept: text/event-stream\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       3333,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Should receive request event first
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
		if httpEvent.Path != "/events" {
			t.Errorf("Expected path /events, got %s", httpEvent.Path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Send SSE response headers
	responseHeaders := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
	responseEvent1 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       3333,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseHeaders)),
	}
	copy(responseEvent1.Buf[:], responseHeaders)
	sm.ProcessTlsEvent(responseEvent1)

	// Send first SSE event chunk
	chunk1 := []byte("1a\r\ndata: {\"type\":\"message\"}\n\n\r\n")
	responseEvent2 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       3333,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(chunk1)),
	}
	copy(responseEvent2.Buf[:], chunk1)
	sm.ProcessTlsEvent(responseEvent2)

	// Check first SSE event was received through channel
	select {
	case sseEvt := <-sm.HTTPEvents():
		// Make a copy of the SSE data
		sseEvent := sseEvt.(*event.SSEEvent)
		dataCopy := make([]byte, len(sseEvent.Data))
		copy(dataCopy, sseEvent.Data)
		sseEvents = append(sseEvents, dataCopy)
		// Clone the SSE event for context
		eventCopy := *sseEvent
		sseContexts = append(sseContexts, &eventCopy)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No SSE event received")
	}

	// Check first SSE event was received
	if len(sseEvents) != 1 {
		t.Fatalf("Expected 1 SSE event, got %d", len(sseEvents))
	}
	// Now we extract just the data portion, not the full SSE format
	expectedData := "{\"type\":\"message\"}"
	if string(sseEvents[0]) != expectedData {
		t.Errorf("Expected SSE event data to be %q, got %q", expectedData, sseEvents[0])
	}

	// Verify HTTP context
	if len(sseContexts) != 1 {
		t.Fatalf("Expected 1 SSE event context, got %d", len(sseContexts))
	}
	sseCtx := sseContexts[0]
	if sseCtx.Method != "GET" {
		t.Errorf("Expected method GET, got %s", sseCtx.Method)
	}
	if sseCtx.Path != "/events" {
		t.Errorf("Expected path /events, got %s", sseCtx.Path)
	}
	if sseCtx.Host != "example.com" {
		t.Errorf("Expected host example.com, got %s", sseCtx.Host)
	}

	// Send second SSE event chunk with multiple events
	chunk2 := []byte("42\r\nevent: update\ndata: {\"id\":1}\n\ndata: {\"id\":2}\n\nid: 123\ndata: test\n\n\r\n")
	responseEvent3 := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       3333,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(chunk2)),
	}
	copy(responseEvent3.Buf[:], chunk2)
	sm.ProcessTlsEvent(responseEvent3)

	// Receive 3 more SSE events from channel
	for i := 0; i < 3; i++ {
		select {
		case sseEvt := <-sm.HTTPEvents():
			// Make a copy of the SSE data
			sseEvent := sseEvt.(*event.SSEEvent)
			dataCopy := make([]byte, len(sseEvent.Data))
			copy(dataCopy, sseEvent.Data)
			sseEvents = append(sseEvents, dataCopy)
			// Clone the SSE event for context
			eventCopy := *sseEvent
			sseContexts = append(sseContexts, &eventCopy)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Expected 3 more SSE events, only got %d", i)
		}
	}

	// Should have received 3 more SSE events (total 4)
	if len(sseEvents) != 4 {
		t.Fatalf("Expected 4 SSE events total, got %d", len(sseEvents))
	}

	// Verify the new events (now containing just the data portion)
	expectedData2 := "{\"id\":1}"
	if string(sseEvents[1]) != expectedData2 {
		t.Errorf("Expected second SSE event data to be %q, got %q", expectedData2, sseEvents[1])
	}
	expectedData3 := "{\"id\":2}"
	if string(sseEvents[2]) != expectedData3 {
		t.Errorf("Expected third SSE event data to be %q, got %q", expectedData3, sseEvents[2])
	}
	expectedData4 := "test"
	if string(sseEvents[3]) != expectedData4 {
		t.Errorf("Expected fourth SSE event data to be %q, got %q", expectedData4, sseEvents[3])
	}

	// No regular HTTP event should be emitted yet (response not complete)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not emit regular HTTP event for ongoing SSE stream")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event
	}
}

func TestSessionManager_SSEWithNonSSEResponse(t *testing.T) {
	// Create session manager
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(88888)

	// Send request
	requestData := []byte("GET /api/data HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       4444,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Should receive request event first
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Send non-SSE chunked response
	responseData := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\nf\r\n{\"status\":\"ok\"}\r\n0\r\n\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       4444,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Regular HTTP response event should be emitted (no SSE events)
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() == event.EventTypeHttpSSE {
			t.Error("Should not receive SSE events for non-SSE responses")
		}
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
		if string(httpEvent.ResponsePayload) != "{\"status\":\"ok\"}" {
			t.Errorf("Expected response payload '{\"status\":\"ok\"}', got %q", httpEvent.ResponsePayload)
		}
		if httpEvent.ResponseHeaders["Content-Type"] != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", httpEvent.ResponseHeaders["Content-Type"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected HTTP response event for non-SSE response")
	}
}

func TestSessionManager_SSECompleteResponse(t *testing.T) {
	// Create session manager
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(55555)

	// Send request
	requestData := []byte("GET /events HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       6666,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Should receive request event first
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Send SSE response with one event (complete response with terminating chunk)
	responseData := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n11\r\ndata: complete\n\n\r\n0\r\n\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       6666,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)
	sm.ProcessTlsEvent(responseEvent)

	// Check SSE event was received through channel
	select {
	case sseEvt := <-sm.HTTPEvents():
		expectedData := "complete"
		sseEvent := sseEvt.(*event.SSEEvent)
		if string(sseEvent.Data) != expectedData {
			t.Errorf("Expected SSE data %q, got %q", expectedData, sseEvent.Data)
		}
		// Verify HTTP context
		if sseEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", sseEvent.Method)
		}
		if sseEvent.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", sseEvent.Host)
		}
		if sseEvent.Path != "/events" {
			t.Errorf("Expected path /events, got %s", sseEvent.Path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No SSE event received through channel")
	}

	// Response should complete and be emitted
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected HTTP response event")
	}
}

func TestSessionManager_SSENoCallback(t *testing.T) {
	// Create session manager without SSE callback
	sm := NewSessionManager()
	defer sm.Close()

	sslCtx := uint64(99999)

	// Send request
	requestData := []byte("GET /events HTTP/1.1\r\nHost: example.com\r\n\r\n")
	requestEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadSend,
			PID:       5555,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(requestData)),
	}
	copy(requestEvent.Buf[:], requestData)
	sm.ProcessTlsEvent(requestEvent)

	// Send SSE response
	responseData := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n18\r\ndata: {\"test\":\"data\"}\n\n\r\n0\r\n\r\n")
	responseEvent := &event.TlsPayloadEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsPayloadRecv,
			PID:       5555,
		},
		SSLContext:  sslCtx,
		HttpVersion: event.HttpVersion1,
		BufSize:     uint32(len(responseData)),
	}
	copy(responseEvent.Buf[:], responseData)

	// Should receive request event first
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpRequest {
			t.Errorf("Expected EventTypeHttpRequest, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpRequestEvent)
		if httpEvent.Method != "GET" {
			t.Errorf("Expected method GET, got %s", httpEvent.Method)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No request event received")
	}

	// Should not panic even without callback
	err := sm.ProcessTlsEvent(responseEvent)
	if err != nil {
		t.Fatalf("ProcessTlsEvent should not fail without SSE callback: %v", err)
	}

	// Should receive SSE event through channel even without callback
	select {
	case sseEvt := <-sm.HTTPEvents():
		expectedData := "{\"test\":\"data\"}"
		sseEvent := sseEvt.(*event.SSEEvent)
		if string(sseEvent.Data) != expectedData {
			t.Errorf("Expected SSE data %q, got %q", expectedData, sseEvent.Data)
		}
		if sseEvent.Method != "GET" {
			t.Errorf("Expected method GET in SSE event, got %s", sseEvent.Method)
		}
		if sseEvent.Path != "/events" {
			t.Errorf("Expected path /events in SSE event, got %s", sseEvent.Path)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should receive SSE event through channel even without callback")
	}

	// Regular HTTP response event should still be emitted when response completes
	select {
	case evt := <-sm.HTTPEvents():
		if evt.Type() != event.EventTypeHttpResponse {
			t.Errorf("Expected EventTypeHttpResponse, got %v", evt.Type())
		}
		httpEvent := evt.(*event.HttpResponseEvent)
		if httpEvent.ResponseHeaders["Content-Type"] != "text/event-stream" {
			t.Errorf("Expected Content-Type text/event-stream, got %s", httpEvent.ResponseHeaders["Content-Type"])
		}
		// The body should contain the SSE data
		if !containsBytes(httpEvent.ResponsePayload, []byte("data: {\"test\":\"data\"}")) {
			t.Errorf("Expected response to contain SSE data, got %q", httpEvent.ResponsePayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Expected HTTP response event for complete SSE response")
	}
}
