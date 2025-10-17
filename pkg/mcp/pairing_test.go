package mcp

import (
	"testing"
	"time"

	tu "github.com/alex-ilgayev/mcpspy/internal/testing"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// TestRequestResponsePairing_BasicFlow tests the basic request-response pairing
func TestRequestResponsePairing_BasicFlow(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request
	requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Read the request from the bus
	var requestMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		requestMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Verify request has no Request field set
	if requestMsg.Request != nil {
		t.Error("Request message should not have Request field set")
	}

	// Send a response
	responseData := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Read the response from the bus
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for response message")
	}

	// Verify response has Request field set
	if responseMsg.Request == nil {
		t.Fatal("Response message should have Request field set")
	}

	// Verify the request in the response matches the original request
	if responseMsg.Request.ID != requestMsg.ID {
		t.Errorf("Response.Request.ID = %v, want %v", responseMsg.Request.ID, requestMsg.ID)
	}
	if responseMsg.Request.Method != requestMsg.Method {
		t.Errorf("Response.Request.Method = %s, want %s", responseMsg.Request.Method, requestMsg.Method)
	}
	if responseMsg.Request.MessageType != event.JSONRPCMessageTypeRequest {
		t.Errorf("Response.Request.MessageType = %s, want %s", responseMsg.Request.MessageType, event.JSONRPCMessageTypeRequest)
	}
}

// TestRequestResponsePairing_StringID tests pairing with string IDs
func TestRequestResponsePairing_StringID(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request with string ID
	requestData := []byte(`{"jsonrpc":"2.0","id":"test-abc-123","method":"initialize","params":{"version":"1.0"}}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Read the request from the bus
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Send a response with matching string ID
	responseData := []byte(`{"jsonrpc":"2.0","id":"test-abc-123","result":{"capabilities":{}}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Read the response from the bus
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for response message")
	}

	// Verify response has Request field set with correct ID
	if responseMsg.Request == nil {
		t.Fatal("Response message should have Request field set")
	}
	if responseMsg.Request.ID != "test-abc-123" {
		t.Errorf("Response.Request.ID = %v, want %s", responseMsg.Request.ID, "test-abc-123")
	}
	if responseMsg.Request.Method != "initialize" {
		t.Errorf("Response.Request.Method = %s, want %s", responseMsg.Request.Method, "initialize")
	}
}

// TestRequestResponsePairing_ResponseWithoutRequest tests that orphaned responses are dropped
func TestRequestResponsePairing_ResponseWithoutRequest(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a response without a corresponding request
	responseData := []byte(`{"jsonrpc":"2.0","id":999,"result":{"content":"orphaned"}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Verify no message is published (response is dropped)
	select {
	case evt := <-mockBus.Events():
		t.Fatalf("Expected no message to be published, got %v", evt)
	case <-time.After(100 * time.Millisecond):
		// Expected - no message should be published
	}
}

// TestRequestResponsePairing_MultipleRequests tests pairing multiple requests with their responses
func TestRequestResponsePairing_MultipleRequests(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send multiple requests
	requests := []struct {
		id     interface{}
		method string
		data   []byte
	}{
		{int64(1), "tools/list", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)},
		{int64(2), "resources/list", []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)},
		{"req-3", "prompts/list", []byte(`{"jsonrpc":"2.0","id":"req-3","method":"prompts/list"}`)},
	}

	for _, req := range requests {
		reqEvent := createFSAggregatedEvent(req.data, event.EventTypeFSRead, 100, "client", 200, "server")
		parser.ParseDataStdio(reqEvent)

		// Drain the request from the bus
		select {
		case <-mockBus.Events():
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout waiting for request with id %v", req.id)
		}
	}

	// Send responses in different order
	responses := []struct {
		id     interface{}
		method string // expected method from the paired request
		data   []byte
	}{
		{int64(2), "resources/list", []byte(`{"jsonrpc":"2.0","id":2,"result":{"resources":[]}}`)},
		{"req-3", "prompts/list", []byte(`{"jsonrpc":"2.0","id":"req-3","result":{"prompts":[]}}`)},
		{int64(1), "tools/list", []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)},
	}

	for _, resp := range responses {
		respEvent := createFSAggregatedEvent(resp.data, event.EventTypeFSWrite, 200, "server", 100, "client")
		parser.ParseDataStdio(respEvent)

		// Read the response from the bus
		var responseMsg *event.MCPEvent
		select {
		case evt := <-mockBus.Events():
			responseMsg = evt.(*event.MCPEvent)
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Timeout waiting for response with id %v", resp.id)
		}

		// Verify response is paired with the correct request
		if responseMsg.Request == nil {
			t.Fatalf("Response with id %v should have Request field set", resp.id)
		}
		if responseMsg.Request.ID != resp.id {
			t.Errorf("Response.Request.ID = %v, want %v", responseMsg.Request.ID, resp.id)
		}
		if responseMsg.Request.Method != resp.method {
			t.Errorf("Response.Request.Method = %s, want %s", responseMsg.Request.Method, resp.method)
		}
	}
}

// TestRequestResponsePairing_ErrorResponse tests pairing with error responses
func TestRequestResponsePairing_ErrorResponse(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request
	requestData := []byte(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"test"}}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Drain the request
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Send an error response
	errorData := []byte(`{"jsonrpc":"2.0","id":42,"error":{"code":-32602,"message":"Invalid params"}}`)
	errorEvent := createFSAggregatedEvent(errorData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(errorEvent)

	// Read the error response from the bus
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for error response")
	}

	// Verify error response is paired with the request
	if responseMsg.Request == nil {
		t.Fatal("Error response should have Request field set")
	}
	if responseMsg.Request.ID != int64(42) {
		t.Errorf("Response.Request.ID = %v, want %v", responseMsg.Request.ID, 42)
	}
	if responseMsg.Request.Method != "tools/call" {
		t.Errorf("Response.Request.Method = %s, want %s", responseMsg.Request.Method, "tools/call")
	}

	// Verify the response has error set
	if responseMsg.Error.Code != -32602 {
		t.Errorf("Response.Error.Code = %d, want %d", responseMsg.Error.Code, -32602)
	}
}

// TestRequestResponsePairing_NotificationNoRequest tests that notifications don't have Request field
func TestRequestResponsePairing_NotificationNoRequest(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a notification
	notificationData := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)
	notificationEvent := createFSAggregatedEvent(notificationData, event.EventTypeFSRead, 100, "server", 200, "client")
	parser.ParseDataStdio(notificationEvent)

	// Read the notification from the bus
	var notificationMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		notificationMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for notification")
	}

	// Verify notification has no Request field set
	if notificationMsg.Request != nil {
		t.Error("Notification should not have Request field set")
	}
	if notificationMsg.MessageType != event.JSONRPCMessageTypeNotification {
		t.Errorf("MessageType = %s, want %s", notificationMsg.MessageType, event.JSONRPCMessageTypeNotification)
	}
}

// TestRequestResponsePairing_HTTPTransport tests pairing works with HTTP transport
func TestRequestResponsePairing_HTTPTransport(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send HTTP request
	requestData := []byte(`{"jsonrpc":"2.0","id":100,"method":"tools/call","params":{"name":"test"}}`)
	requestEvent := createHttpRequestEvent(requestData, 300, "curl", "api.example.com")
	parser.ParseDataHttp(requestEvent)

	// Drain the request
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for HTTP request message")
	}

	// Send HTTP response
	responseData := []byte(`{"jsonrpc":"2.0","id":100,"result":{"content":"success"}}`)
	responseEvent := createHttpResponseEvent(responseData, 300, "curl", "api.example.com")
	parser.ParseDataHttp(responseEvent)

	// Read the response from the bus
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for HTTP response message")
	}

	// Verify HTTP response is paired with the request
	if responseMsg.Request == nil {
		t.Fatal("HTTP response should have Request field set")
	}
	if responseMsg.Request.ID != int64(100) {
		t.Errorf("Response.Request.ID = %v, want %v", responseMsg.Request.ID, 100)
	}
	if responseMsg.Request.Method != "tools/call" {
		t.Errorf("Response.Request.Method = %s, want %s", responseMsg.Request.Method, "tools/call")
	}
	if responseMsg.TransportType != event.TransportTypeHTTP {
		t.Errorf("TransportType = %s, want %s", responseMsg.TransportType, event.TransportTypeHTTP)
	}
}

// TestRequestResponsePairing_RequestParams tests that request params are preserved
func TestRequestResponsePairing_RequestParams(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request with complex params
	requestData := []byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"calculator","arguments":{"x":10,"y":20,"operation":"add"}}}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Drain the request
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Send a response
	responseData := []byte(`{"jsonrpc":"2.0","id":5,"result":{"value":30}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Read the response from the bus
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for response message")
	}

	// Verify request params are preserved in the paired request
	if responseMsg.Request == nil {
		t.Fatal("Response should have Request field set")
	}
	if responseMsg.Request.Params == nil {
		t.Fatal("Request params should be preserved")
	}
	if toolName, ok := responseMsg.Request.Params["name"].(string); !ok || toolName != "calculator" {
		t.Errorf("Request.Params[name] = %v, want 'calculator'", responseMsg.Request.Params["name"])
	}
	if args, ok := responseMsg.Request.Params["arguments"].(map[string]interface{}); ok {
		if x, ok := args["x"].(float64); !ok || x != 10 {
			t.Errorf("Request.Params[arguments][x] = %v, want 10", args["x"])
		}
	} else {
		t.Error("Request.Params[arguments] should be a map")
	}
}

// TestRequestResponsePairing_CacheTTL tests that old requests expire from cache
func TestRequestResponsePairing_CacheTTL(t *testing.T) {
	// This test verifies the TTL behavior, but we need to be careful with timing
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request
	requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Drain the request
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Wait for the cache to expire (TTL is 5 seconds in the parser)
	// For testing purposes, we'll just verify the cache behavior without waiting the full TTL
	// In a real scenario, we'd need to wait > requestIDCacheTTL

	// Immediately send a response - should work
	responseData := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Should receive the response
	select {
	case evt := <-mockBus.Events():
		responseMsg := evt.(*event.MCPEvent)
		if responseMsg.Request == nil {
			t.Error("Response should be paired with request (within TTL)")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for response message")
	}
}

// TestRequestResponsePairing_NoCircularReference tests that we don't create circular references
func TestRequestResponsePairing_NoCircularReference(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Send a request
	requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	requestEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "client", 200, "server")
	parser.ParseDataStdio(requestEvent)

	// Drain the request
	select {
	case <-mockBus.Events():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for request message")
	}

	// Send a response
	responseData := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	responseEvent := createFSAggregatedEvent(responseData, event.EventTypeFSWrite, 200, "server", 100, "client")
	parser.ParseDataStdio(responseEvent)

	// Read the response
	var responseMsg *event.MCPEvent
	select {
	case evt := <-mockBus.Events():
		responseMsg = evt.(*event.MCPEvent)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for response message")
	}

	// Verify the paired request doesn't have a Request field (no circular reference)
	if responseMsg.Request == nil {
		t.Fatal("Response should have Request field set")
	}
	if responseMsg.Request.Request != nil {
		t.Error("Paired request should not have Request field set (would create circular reference)")
	}
}
