package http

import (
	"testing"
	"time"

	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

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

	// Check if event was emitted
	select {
	case event := <-sm.HTTPEvents():
		if event.SSLContext != sslCtx {
			t.Errorf("Expected SSL context %d, got %d", sslCtx, event.SSLContext)
		}
		if event.Method != "GET" {
			t.Errorf("Expected method GET, got %s", event.Method)
		}
		if event.Path != "/api/test" {
			t.Errorf("Expected path /api/test, got %s", event.Path)
		}
		if event.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", event.Host)
		}
		if event.Code != 200 {
			t.Errorf("Expected status code 200, got %d", event.Code)
		}
		if string(event.ResponsePayload) != "Hello, World!" {
			t.Errorf("Expected response payload 'Hello, World!', got %q", event.ResponsePayload)
		}
		if len(event.RequestPayload) != 0 {
			t.Errorf("Expected empty request payload, got %q", event.RequestPayload)
		}
		if event.ResponseHeaders["Content-Length"] != "13" {
			t.Errorf("Expected Content-Length header '13', got %s", event.ResponseHeaders["Content-Length"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No event received")
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

	// Check aggregated event
	select {
	case event := <-sm.HTTPEvents():
		if event.Method != "GET" {
			t.Errorf("Expected method GET, got %s", event.Method)
		}
		if event.Path != "/api/test" {
			t.Errorf("Expected path /api/test, got %s", event.Path)
		}
		if event.Host != "example.com" {
			t.Errorf("Expected host example.com, got %s", event.Host)
		}
		if event.Code != 200 {
			t.Errorf("Expected status code 200, got %d", event.Code)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No event received")
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

	// Verify both events are received
	events := make(map[uint64]*event.HttpEvent)
	for i := 0; i < 2; i++ {
		select {
		case event := <-sm.HTTPEvents():
			events[event.SSLContext] = &event
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected 2 events, timeout waiting")
		}
	}

	// Verify session 1
	if event1, ok := events[sslCtx1]; !ok {
		t.Error("Did not receive event for session 1")
	} else {
		if event1.Path != "/session1" {
			t.Errorf("Session 1: expected path /session1, got %s", event1.Path)
		}
		if string(event1.ResponsePayload) != "session1" {
			t.Errorf("Session 1: expected response payload 'session1', got %q", event1.ResponsePayload)
		}
	}

	// Verify session 2
	if event2, ok := events[sslCtx2]; !ok {
		t.Error("Did not receive event for session 2")
	} else {
		if event2.Path != "/session2" {
			t.Errorf("Session 2: expected path /session2, got %s", event2.Path)
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
				resp := parseHTTPResponse(tt.data, false)
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
			resp := parseHTTPResponse(tt.data, false)

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

	// Check event
	select {
	case event := <-sm.HTTPEvents():
		if !event.IsChunked {
			t.Error("Expected IsChunked to be true")
		}
		if string(event.ResponsePayload) != "hello world" {
			t.Errorf("Expected aggregated response payload 'hello world', got %q", event.ResponsePayload)
		}
		if event.ResponseHeaders["Transfer-Encoding"] != "chunked" {
			t.Errorf("Expected Transfer-Encoding header 'chunked', got %s", event.ResponseHeaders["Transfer-Encoding"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No event received")
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
			body, complete := parseChunkedBody(tt.data, false)
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

	// Should not emit event yet
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not emit event for incomplete chunked response")
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

	// Now should emit event
	select {
	case event := <-sm.HTTPEvents():
		if !event.IsChunked {
			t.Error("Expected IsChunked to be true")
		}
		if string(event.ResponsePayload) != "hello" {
			t.Errorf("Expected response payload 'hello', got %q", event.ResponsePayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No event received")
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

	// Check event
	select {
	case event := <-sm.HTTPEvents():
		if event.Method != "POST" {
			t.Errorf("Expected method POST, got %s", event.Method)
		}
		if event.Path != "/api/users" {
			t.Errorf("Expected path /api/users, got %s", event.Path)
		}
		if event.Host != "api.example.com" {
			t.Errorf("Expected host api.example.com, got %s", event.Host)
		}
		if event.Code != 201 {
			t.Errorf("Expected status code 201, got %d", event.Code)
		}
		expectedReqPayload := "{\"name\":\"John\",\"age\":30}"
		if string(event.RequestPayload) != expectedReqPayload {
			t.Errorf("Expected request payload %q, got %q", expectedReqPayload, event.RequestPayload)
		}
		expectedRespPayload := "{\"id\":\"12345\"}"
		if string(event.ResponsePayload) != expectedRespPayload {
			t.Errorf("Expected response payload %q, got %q", expectedRespPayload, event.ResponsePayload)
		}
		// Check request headers
		if event.RequestHeaders["Content-Type"] != "application/json" {
			t.Errorf("Expected request Content-Type 'application/json', got %s", event.RequestHeaders["Content-Type"])
		}
		if event.RequestHeaders["Content-Length"] != "24" {
			t.Errorf("Expected request Content-Length '24', got %s", event.RequestHeaders["Content-Length"])
		}
		// Check response headers
		if event.ResponseHeaders["Content-Type"] != "application/json" {
			t.Errorf("Expected response Content-Type 'application/json', got %s", event.ResponseHeaders["Content-Type"])
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No event received")
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

	// Should NOT receive an event (only chunked incomplete responses are emitted)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive event for non-chunked incomplete session")
	case <-time.After(100 * time.Millisecond):
		// Expected - no event
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

	// No event should be emitted yet (incomplete)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive event for incomplete chunked response")
	case <-time.After(50 * time.Millisecond):
		// Expected - no event
	}

	// Send TlsFreeEvent to force completion
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       5678,
		},
		SSLContext: sslCtx,
	}
	sm.ProcessTlsFreeEvent(freeEvent)

	// Should receive the incomplete event with partial body
	select {
	case event := <-sm.HTTPEvents():
		if event.Method != "GET" {
			t.Errorf("Expected method GET, got %s", event.Method)
		}
		if event.Code != 200 {
			t.Errorf("Expected status code 200, got %d", event.Code)
		}
		if !event.IsChunked {
			t.Error("Expected chunked response")
		}
		// Should have parsed partial body
		expectedBody := "Hello World"
		if string(event.ResponsePayload) != expectedBody {
			t.Errorf("Expected body %q, got %q", expectedBody, event.ResponsePayload)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should have received incomplete chunked event")
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

	// Send TlsFreeEvent
	freeEvent := &event.TlsFreeEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeTlsFree,
			PID:       2222,
		},
		SSLContext: sslCtx,
	}
	sm.ProcessTlsFreeEvent(freeEvent)

	// Should NOT receive event (only chunked incomplete responses are emitted)
	select {
	case <-sm.HTTPEvents():
		t.Fatal("Should not receive event for session with only request")
	case <-time.After(100 * time.Millisecond):
		// Expected - no event
	}
}
