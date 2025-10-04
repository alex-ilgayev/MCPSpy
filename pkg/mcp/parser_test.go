package mcp

import (
	"fmt"
	"testing"
	"time"

	tu "github.com/alex-ilgayev/mcpspy/internal/testing"
	"github.com/alex-ilgayev/mcpspy/pkg/event"
)

// Helper function to create FSAggregatedEvent for stdio tests
// The parser now expects aggregated events, not raw FS events
func createFSAggregatedEvent(data []byte, eventType event.EventType, fromPID uint32, fromComm string, toPID uint32, toComm string) *event.FSAggregatedEvent {
	var comm [16]uint8
	var fromCommBytes [16]uint8
	var toCommBytes [16]uint8

	copy(comm[:], []byte(fromComm))
	copy(fromCommBytes[:], []byte(fromComm))
	copy(toCommBytes[:], []byte(toComm))

	// Map raw event types to aggregated event types
	aggregatedType := eventType
	if eventType == event.EventTypeFSRead {
		aggregatedType = event.EventTypeFSAggregatedRead
	} else if eventType == event.EventTypeFSWrite {
		aggregatedType = event.EventTypeFSAggregatedWrite
	}

	return event.NewFSAggregatedEvent(
		aggregatedType,
		fromPID,
		comm,
		0, // inode (not needed for tests)
		fromPID,
		fromCommBytes,
		toPID,
		toCommBytes,
		0, // filePtr (not needed for tests)
		data,
	)
}

// Helper function to create HttpRequestEvent for HTTP tests
func createHttpRequestEvent(data []byte, pid uint32, comm string, host string) *event.HttpRequestEvent {
	e := &event.HttpRequestEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpRequest,
			PID:       pid,
		},
		Host:           host,
		RequestPayload: data,
	}
	copy(e.CommBytes[:], []byte(comm))
	return e
}

// Helper function to create HttpResponseEvent for HTTP tests
func createHttpResponseEvent(data []byte, pid uint32, comm string, host string) *event.HttpResponseEvent {
	e := &event.HttpResponseEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpResponse,
			PID:       pid,
		},
		HttpRequestEvent: event.HttpRequestEvent{
			Host: host,
		},
		ResponsePayload: data,
	}
	copy(e.CommBytes[:], []byte(comm))
	return e
}

// Helper function to create SSEEvent for HTTP SSE tests
func createSSEEvent(data []byte, pid uint32, comm string, host string) *event.SSEEvent {
	e := &event.SSEEvent{
		EventHeader: event.EventHeader{
			EventType: event.EventTypeHttpSSE,
			PID:       pid,
		},
		HttpRequestEvent: event.HttpRequestEvent{
			Host: host,
		},
		Data: data,
	}
	copy(e.CommBytes[:], []byte(comm))
	return e
}

func TestParseJSONRPC_ValidMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name           string
		data           []byte
		expectedType   event.JSONRPCMessageType
		expectedMethod string
		expectedID     interface{}
		hasParams      bool
		hasResult      bool
		hasError       bool
	}{
		{
			name:           "Basic request",
			data:           []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     int64(1),
			hasParams:      true,
		},
		{
			name:           "String ID request",
			data:           []byte(`{"jsonrpc":"2.0","id":"test-123","method":"initialize","params":{"version":"1.0.0"}}`),
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "test-123",
			hasParams:      true,
		},
		{
			name:           "Request without params",
			data:           []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`),
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/list",
			expectedID:     int64(2),
		},
		{
			name:         "Success response",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"OK"}]}}`),
			expectedType: event.JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasResult:    true,
		},
		{
			name:         "Error response",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params"}}`),
			expectedType: event.JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasError:     true,
		},
		{
			name:           "Notification",
			data:           []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`),
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		{
			name:           "Notification without params",
			data:           []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create FS event with complete kernel correlation
			fsEvent := createFSAggregatedEvent(tt.data, event.EventTypeFSRead, 100, "writer", 200, "reader")

			// Process the event (publishes to bus)
			parser.ParseDataStdio(fsEvent)

			// Read from bus
			select {
			case evt := <-mockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
				msg := evt.(*event.MCPEvent)

				// Verify correlation fields from kernel
				if msg.StdioTransport == nil {
					t.Fatal("Expected StdioTransport to be set")
				}
				if msg.StdioTransport.FromPID != 100 {
					t.Errorf("Expected FromPID 100, got %d", msg.StdioTransport.FromPID)
				}
				if msg.StdioTransport.FromComm != "writer" {
					t.Errorf("Expected FromComm 'writer', got '%s'", msg.StdioTransport.FromComm)
				}
				if msg.StdioTransport.ToPID != 200 {
					t.Errorf("Expected ToPID 200, got %d", msg.StdioTransport.ToPID)
				}
				if msg.StdioTransport.ToComm != "reader" {
					t.Errorf("Expected ToComm 'reader', got '%s'", msg.StdioTransport.ToComm)
				}

				if msg.MessageType != tt.expectedType {
					t.Errorf("Expected type %s, got %s", tt.expectedType, msg.MessageType)
				}

				if msg.Method != tt.expectedMethod {
					t.Errorf("Expected method %s, got %s", tt.expectedMethod, msg.Method)
				}

				if tt.expectedID != nil && msg.ID != tt.expectedID {
					t.Errorf("Expected ID %v, got %v", tt.expectedID, msg.ID)
				}

				if tt.hasParams && msg.Params == nil {
					t.Error("Expected params to be present")
				}

				if !tt.hasParams && msg.Params != nil {
					t.Error("Expected params to be nil")
				}

				if tt.hasResult && msg.Result == nil {
					t.Error("Expected result to be present")
				}

				if tt.hasError && msg.Error.Code == 0 {
					t.Error("Expected error to be present")
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("No MCP event received")
			}
		})
	}
}

func TestParseJSONRPC_AllSupportedMethods(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test all methods with realistic example messages based on MCP specification
	testCases := []struct {
		method         string
		messageType    string
		data           string
		expectedType   event.JSONRPCMessageType
		expectedMethod string
		expectedID     interface{}
		hasParams      bool
		hasResult      bool
		hasError       bool
		toolName       string // for tools/call
		resourceURI    string // for resources/read, resources/subscribe, etc.
	}{
		// Lifecycle - Client initialization with full capabilities
		{
			method:         "initialize",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"init-001","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":true},"resources":{"subscribe":true,"listChanged":true},"prompts":{"listChanged":true},"logging":{},"experimental":{"textEditor":true}},"clientInfo":{"name":"MCPSpy","version":"1.0.0"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "init-001",
			hasParams:      true,
		},
		// Lifecycle - Server initialization with sampling capabilities
		{
			method:         "initialize",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"init-002","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"sampling":{},"roots":{"listChanged":true}},"clientInfo":{"name":"Claude-Desktop","version":"0.7.1","vendor":"Anthropic"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "init-002",
			hasParams:      true,
		},
		{
			method:         "ping",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"ping-123","method":"ping","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "ping",
			expectedID:     "ping-123",
			hasParams:      true,
		},
		{
			method:         "notifications/initialized",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/initialized",
			hasParams:      true,
		},
		{
			method:         "notifications/cancelled",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"tools-call-456","reason":"Operation timed out after 30 seconds"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/cancelled",
			hasParams:      true,
		},

		// Tools - Basic listing
		{
			method:         "tools/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tools-list-001","method":"tools/list","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/list",
			expectedID:     "tools-list-001",
			hasParams:      true,
		},
		// Tools - File operations tool call
		{
			method:         "tools/call",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tool-call-001","method":"tools/call","params":{"name":"filesystem_operations","arguments":{"action":"read","path":"/home/user/documents/report.md","encoding":"utf-8","max_size":1048576}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     "tool-call-001",
			hasParams:      true,
			toolName:       "filesystem_operations",
		},
		// Tools - Web search tool call
		{
			method:         "tools/call",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tool-call-002","method":"tools/call","params":{"name":"web_search","arguments":{"query":"Model Context Protocol specification","max_results":10,"include_snippets":true,"date_range":"last_month"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     "tool-call-002",
			hasParams:      true,
			toolName:       "web_search",
		},
		// Tools - Database query tool call
		{
			method:         "tools/call",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tool-call-003","method":"tools/call","params":{"name":"database_query","arguments":{"connection":"postgres://localhost:5432/production","query":"SELECT id, name, created_at FROM users WHERE active = true ORDER BY created_at DESC LIMIT 50","timeout":30000}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     "tool-call-003",
			hasParams:      true,
			toolName:       "database_query",
		},
		{
			method:         "notifications/tools/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/tools/list_changed",
			hasParams:      true,
		},

		// Resources - Basic listing
		{
			method:         "resources/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resources-list-001","method":"resources/list","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/list",
			expectedID:     "resources-list-001",
			hasParams:      true,
		},
		// Resources - Template listing
		{
			method:         "resources/templates/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"templates-list-001","method":"resources/templates/list","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/templates/list",
			expectedID:     "templates-list-001",
			hasParams:      true,
		},
		// Resources - File system resource read
		{
			method:         "resources/read",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-read-001","method":"resources/read","params":{"uri":"file:///home/user/projects/mcp-server/config.json"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/read",
			expectedID:     "resource-read-001",
			hasParams:      true,
			resourceURI:    "file:///home/user/projects/mcp-server/config.json",
		},
		// Resources - HTTP resource read
		{
			method:         "resources/read",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-read-002","method":"resources/read","params":{"uri":"https://api.github.com/repos/modelcontextprotocol/specification/contents/README.md"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/read",
			expectedID:     "resource-read-002",
			hasParams:      true,
			resourceURI:    "https://api.github.com/repos/modelcontextprotocol/specification/contents/README.md",
		},
		// Resources - Database resource read
		{
			method:         "resources/read",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-read-003","method":"resources/read","params":{"uri":"postgres://localhost:5432/db/table/users"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/read",
			expectedID:     "resource-read-003",
			hasParams:      true,
			resourceURI:    "postgres://localhost:5432/db/table/users",
		},
		// Resources - Subscribe to file changes
		{
			method:         "resources/subscribe",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-sub-001","method":"resources/subscribe","params":{"uri":"file:///home/user/projects/app/src/**/*.ts"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/subscribe",
			expectedID:     "resource-sub-001",
			hasParams:      true,
			resourceURI:    "file:///home/user/projects/app/src/**/*.ts",
		},
		// Resources - Subscribe to API endpoint
		{
			method:         "resources/subscribe",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-sub-002","method":"resources/subscribe","params":{"uri":"webhook://api.example.com/events/user-activity"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/subscribe",
			expectedID:     "resource-sub-002",
			hasParams:      true,
			resourceURI:    "webhook://api.example.com/events/user-activity",
		},
		{
			method:         "resources/unsubscribe",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-unsub-001","method":"resources/unsubscribe","params":{"uri":"file:///home/user/projects/app/src/**/*.ts"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/unsubscribe",
			expectedID:     "resource-unsub-001",
			hasParams:      true,
			resourceURI:    "file:///home/user/projects/app/src/**/*.ts",
		},
		{
			method:         "notifications/resources/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/resources/list_changed","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/resources/list_changed",
			hasParams:      true,
		},
		{
			method:         "notifications/resources/updated",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///home/user/projects/app/src/main.ts","mimeType":"text/typescript","size":2048,"lastModified":"2025-01-15T14:30:00Z"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/resources/updated",
			hasParams:      true,
		},

		// Prompts - Basic listing
		{
			method:         "prompts/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompts-list-001","method":"prompts/list","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/list",
			expectedID:     "prompts-list-001",
			hasParams:      true,
		},
		// Prompts - Code review prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-001","method":"prompts/get","params":{"name":"code_review","arguments":{"language":"typescript","file":"src/components/UserProfile.tsx","focus":"security","max_suggestions":5}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-001",
			hasParams:      true,
		},
		// Prompts - Documentation generation prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-002","method":"prompts/get","params":{"name":"generate_documentation","arguments":{"codebase_path":"/home/user/projects/mcp-server","output_format":"markdown","include_examples":true,"target_audience":"developers"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-002",
			hasParams:      true,
		},
		// Prompts - Bug analysis prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-003","method":"prompts/get","params":{"name":"analyze_bug_report","arguments":{"error_message":"TypeError: Cannot read property 'id' of undefined","stack_trace":"at UserService.getUserById (user.service.ts:42:15)","reproduction_steps":"1. Login as admin\n2. Navigate to user list\n3. Click on deleted user","severity":"high"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-003",
			hasParams:      true,
		},
		// Completion - Code completion
		{
			method:         "completion/complete",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"completion-001","method":"completion/complete","params":{"ref":{"type":"ref","name":"typescript_completion"},"argument":{"name":"context","value":"async function processUserData(users: User[]) {\n  // Complete this function to validate and transform user data\n  "}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "completion/complete",
			expectedID:     "completion-001",
			hasParams:      true,
		},
		// Completion - SQL query completion
		{
			method:         "completion/complete",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"completion-002","method":"completion/complete","params":{"ref":{"type":"ref","name":"sql_completion"},"argument":{"name":"partial_query","value":"SELECT u.name, u.email, COUNT(o.id) as order_count FROM users u LEFT JOIN orders o ON u.id = o.user_id WHERE u.created_at > '2024-01-01' GROUP BY"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "completion/complete",
			expectedID:     "completion-002",
			hasParams:      true,
		},
		{
			method:         "notifications/prompts/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/prompts/list_changed","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/prompts/list_changed",
			hasParams:      true,
		},

		// Progress notifications - File processing
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"file-analysis-001","progress":0.35,"total":1.0,"message":"Analyzing TypeScript files...","detail":"Processing src/components/UserProfile.tsx (142/400 files)"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		// Progress notifications - Database operation
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"db-migration-002","progress":0.82,"total":1.0,"message":"Running database migration...","detail":"Migrating table users: 82,450/100,000 records"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		// Progress notifications - Tool execution
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"web-scrape-003","progress":0.67,"total":1.0,"message":"Scraping web pages...","detail":"Downloaded 201/300 pages, current: https://docs.example.com/api/users"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},

		// Logging - Set debug level
		{
			method:         "logging/setLevel",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"logging-001","method":"logging/setLevel","params":{"level":"debug"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "logging/setLevel",
			expectedID:     "logging-001",
			hasParams:      true,
		},
		// Logging - Set error level
		{
			method:         "logging/setLevel",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"logging-002","method":"logging/setLevel","params":{"level":"error"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "logging/setLevel",
			expectedID:     "logging-002",
			hasParams:      true,
		},
		// Logging - Info message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"MCP server initialized successfully with 15 tools and 42 resources","logger":"mcp-filesystem-server","timestamp":"2025-01-15T14:30:15.123Z"}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},
		// Logging - Error message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"error","data":"Failed to connect to database: connection timeout after 30s","logger":"mcp-database-server","timestamp":"2025-01-15T14:30:45.567Z","extra":{"host":"localhost:5432","database":"production","retry_count":3}}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},
		// Logging - Debug message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"debug","data":"Tool execution completed","logger":"mcp-tools-server","timestamp":"2025-01-15T14:30:50.890Z","extra":{"tool_name":"web_search","execution_time_ms":1547,"result_size":2048,"cache_hit":false}}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},

		// Client capabilities - Sampling with code analysis
		{
			method:         "sampling/createMessage",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"sampling-001","method":"sampling/createMessage","params":{"messages":[{"role":"user","content":{"type":"text","text":"Please analyze this TypeScript code for potential security vulnerabilities and suggest improvements"}},{"role":"user","content":{"type":"resource","resource":{"uri":"file:///home/user/project/src/auth.ts","mimeType":"text/typescript"}}}],"modelPreferences":{"costPriority":0.8,"speedPriority":0.2,"intelligencePriority":0.9},"systemPrompt":"You are a senior security engineer reviewing code for production deployment."}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "sampling/createMessage",
			expectedID:     "sampling-001",
			hasParams:      true,
		},
		// Client capabilities - Sampling with conversation context
		{
			method:         "sampling/createMessage",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"sampling-002","method":"sampling/createMessage","params":{"messages":[{"role":"user","content":{"type":"text","text":"I need help debugging this error"}},{"role":"assistant","content":{"type":"text","text":"I'd be happy to help! Could you share the error message and relevant code?"}},{"role":"user","content":{"type":"text","text":"Here's the error: Cannot read property 'id' of undefined. The code is in UserService.getUserById method."}}],"modelPreferences":{"hints":[{"name":"temperature","value":0.3},{"name":"max_tokens","value":1000}]},"includeContext":"conversation"}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "sampling/createMessage",
			expectedID:     "sampling-002",
			hasParams:      true,
		},
		// Client capabilities - User input elicitation for API keys
		{
			method:         "elicitation/create",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"elicit-001","method":"elicitation/create","params":{"prompt":"Please provide your GitHub API token to access private repositories","inputType":"password","placeholder":"ghp_xxxxxxxxxxxxxxxxxxxx","validation":{"pattern":"^ghp_[A-Za-z0-9_]{36}$","errorMessage":"Invalid GitHub token format"}}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "elicitation/create",
			expectedID:     "elicit-001",
			hasParams:      true,
		},
		// Client capabilities - User input elicitation for configuration
		{
			method:         "elicitation/create",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"elicit-002","method":"elicitation/create","params":{"prompt":"Enter the database connection string for your development environment","inputType":"text","placeholder":"postgresql://username:password@localhost:5432/dbname","required":true,"description":"This will be used to connect to your local PostgreSQL database for development."}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "elicitation/create",
			expectedID:     "elicit-002",
			hasParams:      true,
		},
		// Client capabilities - Roots listing
		{
			method:         "roots/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"roots-list-001","method":"roots/list","params":{}}`,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "roots/list",
			expectedID:     "roots-list-001",
			hasParams:      true,
		},
		{
			method:         "notifications/roots/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/roots/list_changed","params":{"added":[{"uri":"file:///home/user/new-project","name":"New Project"}],"removed":["file:///home/user/old-project"]}}`,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/roots/list_changed",
			hasParams:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			data := []byte(tc.data)

			// Create FS event with complete kernel correlation
			fsEvent := createFSAggregatedEvent(data, event.EventTypeFSRead, 100, "writer", 200, "reader")

			// Process the event (publishes to bus)
			parser.ParseDataStdio(fsEvent)

			// Read from bus
			select {
			case evt := <-mockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
				msg := evt.(*event.MCPEvent)

				// Validate message type
				if msg.MessageType != tc.expectedType {
					t.Errorf("Expected type %s, got %s", tc.expectedType, msg.MessageType)
				}

				// Validate method name
				if msg.Method != tc.expectedMethod {
					t.Errorf("Expected method %s, got %s", tc.expectedMethod, msg.Method)
				}

				// Validate ID for requests
				if tc.expectedID != nil && msg.ID != tc.expectedID {
					t.Errorf("Expected ID %v, got %v", tc.expectedID, msg.ID)
				}

				// Validate ID presence/absence based on message type
				if tc.messageType == "request" && msg.ID == nil {
					t.Errorf("Request message %s should have ID", tc.method)
				}

				if tc.messageType == "notification" && msg.ID != nil {
					t.Errorf("Notification message %s should not have ID", tc.method)
				}

				// Validate parameters
				if tc.hasParams && msg.Params == nil {
					t.Error("Expected params to be present")
				}

				if !tc.hasParams && msg.Params != nil {
					t.Error("Expected params to be nil")
				}

				// Validate result (for response messages)
				if tc.hasResult && msg.Result == nil {
					t.Error("Expected result to be present")
				}

				// Validate error (for error response messages)
				if tc.hasError && msg.Error.Code == 0 {
					t.Error("Expected error to be present")
				}

				// Validate tool name extraction
				if tc.toolName != "" {
					toolName := msg.ExtractToolName()
					if toolName != tc.toolName {
						t.Errorf("Expected tool name '%s', got '%s'", tc.toolName, toolName)
					}
				}

				// Validate resource URI extraction
				if tc.resourceURI != "" {
					resourceURI := msg.ExtractResourceURI()
					if resourceURI != tc.resourceURI {
						t.Errorf("Expected resource URI '%s', got '%s'", tc.resourceURI, resourceURI)
					}
				}

				// Additional validation for realistic message structure
				if tc.hasParams && msg.Params == nil {
					t.Error("Message marked as having params but params are nil")
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("No MCP event received")
			}
		})
	}
}

func TestParseJSONRPC_InvalidMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name        string
		data        []byte
		expectError string
	}{
		{
			name:        "Missing jsonrpc field",
			data:        []byte(`{"id":1,"method":"tools/call"}`),
			expectError: "not JSON-RPC 2.0",
		},
		{
			name:        "Wrong jsonrpc version",
			data:        []byte(`{"jsonrpc":"1.0","id":1,"method":"tools/call"}`),
			expectError: "not JSON-RPC 2.0",
		},
		{
			name:        "Unknown method",
			data:        []byte(`{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`),
			expectError: "unknown MCP method",
		},
		{
			name:        "Response without ID",
			data:        []byte(`{"jsonrpc":"2.0","result":{"status":"ok"}}`),
			expectError: "unknown JSON-RPC message type",
		},
		{
			name:        "Unknown notification method",
			data:        []byte(`{"jsonrpc":"2.0","method":"unknown/notification"}`),
			expectError: "unknown MCP method",
		},
		{
			name:        "Ambiguous message (no method, no result/error)",
			data:        []byte(`{"jsonrpc":"2.0","id":1}`),
			expectError: "unknown JSON-RPC message type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create FS event with complete kernel correlation
			fsEvent := createFSAggregatedEvent(tt.data, event.EventTypeFSRead, 100, "writer", 200, "reader")

			// Process the event (should not publish due to error)
			parser.ParseDataStdio(fsEvent)

			// Check that NO event was published (timeout = expected behavior for errors)
			select {
			case evt := <-mockBus.Events():
				t.Errorf("Expected no event for invalid message, but got event of type %v", evt.Type())
			case <-time.After(50 * time.Millisecond):
				// Success - no event published for invalid message
			}
		})
	}
}

func TestParseData_KernelCorrelation(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)

	// Test normal flow with complete kernel correlation
	t.Run("Complete kernel correlation", func(t *testing.T) {
		// Kernel provides complete correlation
		fsEvent := createFSAggregatedEvent(data, event.EventTypeFSRead, 100, "writer", 200, "reader")
		parser.ParseDataStdio(fsEvent)

		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
			msg := evt.(*event.MCPEvent)

			if msg.StdioTransport == nil {
				t.Fatal("Expected StdioTransport to be set")
			}
			if msg.StdioTransport.FromPID != 100 {
				t.Errorf("Expected FromPID 100, got %d", msg.StdioTransport.FromPID)
			}
			if msg.StdioTransport.FromComm != "writer" {
				t.Errorf("Expected FromComm 'writer', got '%s'", msg.StdioTransport.FromComm)
			}
			if msg.StdioTransport.ToPID != 200 {
				t.Errorf("Expected ToPID 200, got %d", msg.StdioTransport.ToPID)
			}
			if msg.StdioTransport.ToComm != "reader" {
				t.Errorf("Expected ToComm 'reader', got '%s'", msg.StdioTransport.ToComm)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received")
		}
	})

	// Test incomplete correlation (kernel couldn't correlate)
	t.Run("Incomplete correlation - missing from", func(t *testing.T) {
		mockBus2 := tu.NewMockBus()
		newParser, err := NewParser(mockBus2)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer mockBus2.Close()

		data2 := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

		// Kernel couldn't find writer (fromPID=0), but parser still processes it
		fsEvent := createFSAggregatedEvent(data2, event.EventTypeFSRead, 0, "", 200, "reader")
		newParser.ParseDataStdio(fsEvent)

		select {
		case evt := <-mockBus2.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
			msg := evt.(*event.MCPEvent)
			// Verify incomplete correlation is reflected in the message
			if msg.StdioTransport.FromPID != 0 {
				t.Errorf("Expected FromPID to be 0, got %d", msg.StdioTransport.FromPID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected 1 message (processed despite incomplete correlation)")
		}
	})

	// Test incomplete correlation (kernel couldn't correlate)
	t.Run("Incomplete correlation - missing to", func(t *testing.T) {
		mockBus3 := tu.NewMockBus()
		newParser, err := NewParser(mockBus3)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer mockBus3.Close()

		data2 := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)

		// Kernel couldn't find reader (toPID=0), but parser still processes it
		fsEvent := createFSAggregatedEvent(data2, event.EventTypeFSRead, 100, "writer", 0, "")
		newParser.ParseDataStdio(fsEvent)

		select {
		case evt := <-mockBus3.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
			msg := evt.(*event.MCPEvent)
			// Verify incomplete correlation is reflected in the message
			if msg.StdioTransport.ToPID != 0 {
				t.Errorf("Expected ToPID to be 0, got %d", msg.StdioTransport.ToPID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Expected 1 message (processed despite incomplete correlation)")
		}
	})
}

func TestParseData_MultipleMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test multiple JSON messages separated by newlines
	multipleData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)

	// Parse with complete kernel correlation
	fsEvent := createFSAggregatedEvent(multipleData, event.EventTypeFSRead, 100, "writer", 200, "reader")
	parser.ParseDataStdio(fsEvent)

	// Verify message types
	expectedTypes := []event.JSONRPCMessageType{
		event.JSONRPCMessageTypeRequest,
		event.JSONRPCMessageTypeRequest,
		event.JSONRPCMessageTypeNotification,
	}

	// Read all 3 messages from the bus
	for i, expectedType := range expectedTypes {
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Message %d: Expected EventTypeMCPMessage, got %v", i, evt.Type())
			}
			msg := evt.(*event.MCPEvent)
			if msg.MessageType != expectedType {
				t.Errorf("Message %d: expected type %s, got %s", i, expectedType, msg.MessageType)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Expected message %d, but timed out", i)
		}
	}

	// Verify no more messages
	select {
	case evt := <-mockBus.Events():
		t.Errorf("Expected only 3 messages, but got extra event of type %v", evt.Type())
	case <-time.After(50 * time.Millisecond):
		// Success - no more messages
	}
}

func TestExtractToolName(t *testing.T) {
	tests := []struct {
		name     string
		msg      *event.MCPEvent
		expected string
	}{
		{
			name: "Valid tool call",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/call",
					Params: map[string]interface{}{
						"name": "web_scrape",
						"args": map[string]interface{}{"url": "https://example.com"},
					},
				},
			},
			expected: "web_scrape",
		},
		{
			name: "Tool call with no name",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/call",
					Params: map[string]interface{}{
						"args": map[string]interface{}{"url": "https://example.com"},
					},
				},
			},
			expected: "",
		},
		{
			name: "Non-tool method",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/list",
				},
			},
			expected: "",
		},
		{
			name: "No params",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/call",
				},
			},
			expected: "",
		},
		{
			name: "Non-string name",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/call",
					Params: map[string]interface{}{
						"name": 123,
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.msg.ExtractToolName()
			if result != tt.expected {
				t.Errorf("Expected tool name '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestExtractResourceURI(t *testing.T) {
	tests := []struct {
		name     string
		msg      *event.MCPEvent
		expected string
	}{
		{
			name: "Valid resource read",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "resources/read",
					Params: map[string]interface{}{
						"uri": "config://server",
					},
				},
			},
			expected: "config://server",
		},
		{
			name: "Resource read with no URI",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "resources/read",
					Params: map[string]interface{}{
						"other": "value",
					},
				},
			},
			expected: "",
		},
		{
			name: "Non-resource method",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "tools/call",
				},
			},
			expected: "",
		},
		{
			name: "No params",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "resources/read",
				},
			},
			expected: "",
		},
		{
			name: "Non-string URI",
			msg: &event.MCPEvent{
				JSONRPCMessage: event.JSONRPCMessage{
					Method: "resources/read",
					Params: map[string]interface{}{
						"uri": 123,
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.msg.ExtractResourceURI()
			if result != tt.expected {
				t.Errorf("Expected resource URI '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGetMethodDescription(t *testing.T) {
	tests := []struct {
		method   string
		expected string
	}{
		{"tools/call", "Execute a tool"},
		{"resources/read", "Read a resource"},
		{"initialize", "Initialize connection"},
		{"notifications/progress", "Progress update"},
		{"unknown/method", "Unknown method"},
		{"", "Unknown method"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			result := GetMethodDescription(tt.method)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestParseData_UnsupportedEventType(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Note: With the new architecture, unsupported event types are simply ignored by the parser
	// because the parser only subscribes to specific event types (FSRead, FSWrite, HttpRequest, HttpResponse, HttpSSE)
	// This test is now a no-op, but we keep it for documentation purposes

	// The parser will not process events it didn't subscribe to
	t.Log("Unsupported event types are now filtered at the subscription level")
}

func TestParseDataHttp_ValidMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name           string
		data           []byte
		eventType      event.EventType
		expectedType   event.JSONRPCMessageType
		expectedMethod string
		expectedID     interface{}
		hasParams      bool
		hasResult      bool
		hasError       bool
	}{
		{
			name:           "HTTP Request - Basic request",
			data:           []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
			eventType:      event.EventTypeHttpRequest,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     int64(1),
			hasParams:      true,
		},
		{
			name:         "HTTP Response - Success",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"OK"}]}}`),
			eventType:    event.EventTypeHttpResponse,
			expectedType: event.JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasResult:    true,
		},
		{
			name:           "HTTP SSE - Notification",
			data:           []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`),
			eventType:      event.EventTypeHttpSSE,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		{
			name:           "HTTP Request - Initialize",
			data:           []byte(`{"jsonrpc":"2.0","id":"init-001","method":"initialize","params":{"version":"1.0.0"}}`),
			eventType:      event.EventTypeHttpRequest,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "init-001",
			hasParams:      true,
		},
		{
			name:         "HTTP Response - Error",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params"}}`),
			eventType:    event.EventTypeHttpResponse,
			expectedType: event.JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var httpEvent event.Event
			switch tt.eventType {
			case event.EventTypeHttpRequest:
				httpEvent = createHttpRequestEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpResponse:
				httpEvent = createHttpResponseEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpSSE:
				httpEvent = createSSEEvent(tt.data, 100, "http-process", "example.com")
			}

			parser.ParseDataHttp(httpEvent)

			select {
			case evt := <-mockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
				msg := evt.(*event.MCPEvent)

				// Check transport type
				if msg.TransportType != event.TransportTypeHTTP {
					t.Errorf("Expected transport type HTTP, got %s", msg.TransportType)
				}

				// Check that stdio transport is nil for HTTP messages
				if msg.StdioTransport != nil {
					t.Error("Expected StdioTransport to be nil for HTTP transport")
				}

				// Check that HttpTransport is populated correctly
				if msg.HttpTransport == nil {
					t.Error("Expected HttpTransport to be populated for HTTP transport")
				} else {
					if msg.HttpTransport.PID != 100 {
						t.Errorf("Expected HttpTransport.PID 100, got %d", msg.HttpTransport.PID)
					}
					if msg.HttpTransport.Comm != "http-process" {
						t.Errorf("Expected HttpTransport.Comm 'http-process', got '%s'", msg.HttpTransport.Comm)
					}
					if msg.HttpTransport.Host != "example.com" {
						t.Errorf("Expected HttpTransport.Host 'example.com', got '%s'", msg.HttpTransport.Host)
					}
					// IsRequest is automatically set based on event type:
					// HttpRequest => true, HttpResponse => false, SSE => false
					expectedIsRequest := (tt.eventType == event.EventTypeHttpRequest)
					if msg.HttpTransport.IsRequest != expectedIsRequest {
						t.Errorf("Expected HttpTransport.IsRequest %t, got %t", expectedIsRequest, msg.HttpTransport.IsRequest)
					}
				}

				if msg.MessageType != tt.expectedType {
					t.Errorf("Expected type %s, got %s", tt.expectedType, msg.MessageType)
				}

				if msg.Method != tt.expectedMethod {
					t.Errorf("Expected method %s, got %s", tt.expectedMethod, msg.Method)
				}

				if tt.expectedID != nil && msg.ID != tt.expectedID {
					t.Errorf("Expected ID %v, got %v", tt.expectedID, msg.ID)
				}

				if tt.hasParams && msg.Params == nil {
					t.Error("Expected params to be present")
				}

				if !tt.hasParams && msg.Params != nil {
					t.Error("Expected params to be nil")
				}

				if tt.hasResult && msg.Result == nil {
					t.Error("Expected result to be present")
				}

				if tt.hasError && msg.Error.Code == 0 {
					t.Error("Expected error to be present")
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("No MCP event received")
			}
		})
	}
}

func TestParseDataHttp_MultipleMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test multiple JSON messages separated by newlines
	multipleData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)

	httpEvent := createHttpRequestEvent(multipleData, 100, "http-process", "example.com")
	parser.ParseDataHttp(httpEvent)

	// Verify message types
	expectedTypes := []event.JSONRPCMessageType{
		event.JSONRPCMessageTypeRequest,
		event.JSONRPCMessageTypeRequest,
		event.JSONRPCMessageTypeNotification,
	}

	// Read all 3 messages from the bus
	for i, expectedType := range expectedTypes {
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Message %d: Expected EventTypeMCPMessage, got %v", i, evt.Type())
			}
			msg := evt.(*event.MCPEvent)

			if msg.MessageType != expectedType {
				t.Errorf("Message %d: expected type %s, got %s", i, expectedType, msg.MessageType)
			}

			// All should have HTTP transport type
			if msg.TransportType != event.TransportTypeHTTP {
				t.Errorf("Message %d: expected transport type HTTP, got %s", i, msg.TransportType)
			}

			// Check HttpTransport fields for each message
			if msg.HttpTransport == nil {
				t.Errorf("Message %d: expected HttpTransport to be populated", i)
			} else {
				if msg.HttpTransport.PID != 100 {
					t.Errorf("Message %d: expected HttpTransport.PID 100, got %d", i, msg.HttpTransport.PID)
				}
				if msg.HttpTransport.Comm != "http-process" {
					t.Errorf("Message %d: expected HttpTransport.Comm 'http-process', got '%s'", i, msg.HttpTransport.Comm)
				}
				if msg.HttpTransport.Host != "example.com" {
					t.Errorf("Message %d: expected HttpTransport.Host 'example.com', got '%s'", i, msg.HttpTransport.Host)
				}
				// IsRequest is true for HttpRequestEvent, false for others
				if msg.HttpTransport.IsRequest != true {
					t.Errorf("Message %d: expected HttpTransport.IsRequest true, got %t", i, msg.HttpTransport.IsRequest)
				}
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Expected message %d, but timed out", i)
		}
	}

	// Verify no more messages
	select {
	case evt := <-mockBus.Events():
		t.Errorf("Expected only 3 messages, but got extra event of type %v", evt.Type())
	case <-time.After(50 * time.Millisecond):
		// Success - no more messages
	}
}

func TestParseDataHttp_InvalidMessages(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name        string
		data        []byte
		eventType   event.EventType
		expectError string
	}{
		{
			name:        "Invalid JSON",
			data:        []byte(`{"invalid": json}`),
			eventType:   event.EventTypeHttpRequest,
			expectError: "failed to parse JSON-RPC: invalid JSON",
		},
		{
			name:        "Missing jsonrpc field",
			data:        []byte(`{"id":1,"method":"tools/call"}`),
			eventType:   event.EventTypeHttpResponse,
			expectError: "failed to parse JSON-RPC: not JSON-RPC 2.0",
		},
		{
			name:        "Wrong jsonrpc version",
			data:        []byte(`{"jsonrpc":"1.0","id":1,"method":"tools/call"}`),
			eventType:   event.EventTypeHttpSSE,
			expectError: "failed to parse JSON-RPC: not JSON-RPC 2.0",
		},
		{
			name:        "Unknown method",
			data:        []byte(`{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`),
			eventType:   event.EventTypeHttpRequest,
			expectError: "invalid MCP message: unknown MCP method",
		},
		{
			name:        "Response without ID",
			data:        []byte(`{"jsonrpc":"2.0","result":{"status":"ok"}}`),
			eventType:   event.EventTypeHttpResponse,
			expectError: "failed to parse JSON-RPC: unknown JSON-RPC message type",
		},
		{
			name:        "Unknown notification method",
			data:        []byte(`{"jsonrpc":"2.0","method":"unknown/notification"}`),
			eventType:   event.EventTypeHttpSSE,
			expectError: "invalid MCP message: unknown MCP method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var httpEvent event.Event
			switch tt.eventType {
			case event.EventTypeHttpRequest:
				httpEvent = createHttpRequestEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpResponse:
				httpEvent = createHttpResponseEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpSSE:
				httpEvent = createSSEEvent(tt.data, 100, "http-process", "example.com")
			}

			parser.ParseDataHttp(httpEvent)

			// Check that NO event was published (timeout = expected behavior for errors)
			select {
			case evt := <-mockBus.Events():
				t.Errorf("Expected no event for invalid message, but got event of type %v", evt.Type())
			case <-time.After(50 * time.Millisecond):
				// Success - no event published for invalid message
			}
		})
	}
}

func TestParseDataHttp_EmptyData(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name      string
		data      []byte
		eventType event.EventType
	}{
		{
			name:      "Empty string",
			data:      []byte(""),
			eventType: event.EventTypeHttpRequest,
		},
		{
			name:      "Only whitespace",
			data:      []byte("   \n\t\n   "),
			eventType: event.EventTypeHttpResponse,
		},
		{
			name:      "Only newlines",
			data:      []byte("\n\n\n"),
			eventType: event.EventTypeHttpSSE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var httpEvent event.Event
			switch tt.eventType {
			case event.EventTypeHttpRequest:
				httpEvent = createHttpRequestEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpResponse:
				httpEvent = createHttpResponseEvent(tt.data, 100, "http-process", "example.com")
			case event.EventTypeHttpSSE:
				httpEvent = createSSEEvent(tt.data, 100, "http-process", "example.com")
			}

			parser.ParseDataHttp(httpEvent)

			// Check that NO event was published (empty data should produce no messages)
			select {
			case evt := <-mockBus.Events():
				t.Errorf("Expected 0 messages for empty data, got event of type %v", evt.Type())
			case <-time.After(50 * time.Millisecond):
				// Success - no messages for empty data
			}
		})
	}
}

func TestParseDataHttp_AllSupportedMethods(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test a subset of methods with HTTP transport
	testCases := []struct {
		method         string
		data           string
		eventType      event.EventType
		expectedType   event.JSONRPCMessageType
		expectedMethod string
		expectedID     interface{}
	}{
		{
			method:         "initialize",
			data:           `{"jsonrpc":"2.0","id":"http-init-001","method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
			eventType:      event.EventTypeHttpRequest,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "http-init-001",
		},
		{
			method:         "tools/call",
			data:           `{"jsonrpc":"2.0","id":"http-tool-001","method":"tools/call","params":{"name":"web_search"}}`,
			eventType:      event.EventTypeHttpRequest,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     "http-tool-001",
		},
		{
			method:         "resources/read",
			data:           `{"jsonrpc":"2.0","id":"http-res-001","method":"resources/read","params":{"uri":"https://api.example.com/data"}}`,
			eventType:      event.EventTypeHttpRequest,
			expectedType:   event.JSONRPCMessageTypeRequest,
			expectedMethod: "resources/read",
			expectedID:     "http-res-001",
		},
		{
			method:         "notifications/progress",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":75}}`,
			eventType:      event.EventTypeHttpSSE,
			expectedType:   event.JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.method+"_HTTP", func(t *testing.T) {
			data := []byte(tc.data)

			var httpEvent event.Event
			switch tc.eventType {
			case event.EventTypeHttpRequest:
				httpEvent = createHttpRequestEvent(data, 100, "http-process", "example.com")
			case event.EventTypeHttpResponse:
				httpEvent = createHttpResponseEvent(data, 100, "http-process", "example.com")
			case event.EventTypeHttpSSE:
				httpEvent = createSSEEvent(data, 100, "http-process", "example.com")
			}

			parser.ParseDataHttp(httpEvent)

			select {
			case evt := <-mockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
				msg := evt.(*event.MCPEvent)

				// Validate transport type
				if msg.TransportType != event.TransportTypeHTTP {
					t.Errorf("Expected transport type HTTP, got %s", msg.TransportType)
				}

				// Validate message type
				if msg.MessageType != tc.expectedType {
					t.Errorf("Expected type %s, got %s", tc.expectedType, msg.MessageType)
				}

				// Validate method name
				if msg.Method != tc.expectedMethod {
					t.Errorf("Expected method %s, got %s", tc.expectedMethod, msg.Method)
				}

				// Validate ID
				if tc.expectedID != nil && msg.ID != tc.expectedID {
					t.Errorf("Expected ID %v, got %v", tc.expectedID, msg.ID)
				}

				// Validate HttpTransport fields
				if msg.HttpTransport == nil {
					t.Error("Expected HttpTransport to be populated for HTTP transport")
				} else {
					if msg.HttpTransport.PID != 100 {
						t.Errorf("Expected HttpTransport.PID 100, got %d", msg.HttpTransport.PID)
					}
					if msg.HttpTransport.Comm != "http-process" {
						t.Errorf("Expected HttpTransport.Comm 'http-process', got '%s'", msg.HttpTransport.Comm)
					}
					if msg.HttpTransport.Host != "example.com" {
						t.Errorf("Expected HttpTransport.Host 'example.com', got '%s'", msg.HttpTransport.Host)
					}
					// IsRequest is automatically set based on event type
					expectedIsRequest := (tc.eventType == event.EventTypeHttpRequest)
					if msg.HttpTransport.IsRequest != expectedIsRequest {
						t.Errorf("Expected HttpTransport.IsRequest %t, got %t", expectedIsRequest, msg.HttpTransport.IsRequest)
					}
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("No MCP event received")
			}
		})
	}
}

func TestParseDataHttp_HttpTransportFields(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	tests := []struct {
		name      string
		data      []byte
		eventType event.EventType
		pid       uint32
		comm      string
		host      string
		isRequest bool
	}{
		{
			name:      "HTTP Request with custom fields",
			data:      []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
			eventType: event.EventTypeHttpRequest,
			pid:       1234,
			comm:      "custom-server",
			host:      "api.example.org",
			isRequest: true,
		},
		{
			name:      "HTTP Response with different fields",
			data:      []byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`),
			eventType: event.EventTypeHttpResponse,
			pid:       5678,
			comm:      "backend-service",
			host:      "internal.api.com",
			isRequest: false,
		},
		{
			name:      "HTTP SSE with localhost",
			data:      []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":75}}`),
			eventType: event.EventTypeHttpSSE,
			pid:       9999,
			comm:      "mcp-client",
			host:      "localhost:8080",
			isRequest: true,
		},
		{
			name:      "HTTP Request with empty host",
			data:      []byte(`{"jsonrpc":"2.0","id":"test-id","method":"initialize","params":{}}`),
			eventType: event.EventTypeHttpRequest,
			pid:       100,
			comm:      "test-process",
			host:      "",
			isRequest: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cache request ID for responses
			if tt.eventType == event.EventTypeHttpResponse {
				parser.cacheRequestID(int64(1))
			}

			var httpEvent event.Event
			switch tt.eventType {
			case event.EventTypeHttpRequest:
				e := &event.HttpRequestEvent{
					EventHeader: event.EventHeader{
						EventType: event.EventTypeHttpRequest,
						PID:       tt.pid,
					},
					Host:           tt.host,
					RequestPayload: tt.data,
				}
				copy(e.CommBytes[:], []byte(tt.comm))
				httpEvent = e
			case event.EventTypeHttpResponse:
				e := &event.HttpResponseEvent{
					EventHeader: event.EventHeader{
						EventType: event.EventTypeHttpResponse,
						PID:       tt.pid,
					},
					HttpRequestEvent: event.HttpRequestEvent{
						Host: tt.host,
					},
					ResponsePayload: tt.data,
				}
				copy(e.CommBytes[:], []byte(tt.comm))
				httpEvent = e
			case event.EventTypeHttpSSE:
				e := &event.SSEEvent{
					EventHeader: event.EventHeader{
						EventType: event.EventTypeHttpSSE,
						PID:       tt.pid,
					},
					HttpRequestEvent: event.HttpRequestEvent{
						Host: tt.host,
					},
					Data: tt.data,
				}
				copy(e.CommBytes[:], []byte(tt.comm))
				httpEvent = e
			}

			parser.ParseDataHttp(httpEvent)

			select {
			case evt := <-mockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
				msg := evt.(*event.MCPEvent)

				// Check transport type
				if msg.TransportType != event.TransportTypeHTTP {
					t.Errorf("Expected transport type HTTP, got %s", msg.TransportType)
				}

				// Check that stdio transport is nil for HTTP messages
				if msg.StdioTransport != nil {
					t.Error("Expected StdioTransport to be nil for HTTP transport")
				}

				// Validate all HttpTransport fields
				if msg.HttpTransport == nil {
					t.Fatal("Expected HttpTransport to be populated for HTTP transport")
				}

				if msg.HttpTransport.PID != tt.pid {
					t.Errorf("Expected HttpTransport.PID %d, got %d", tt.pid, msg.HttpTransport.PID)
				}

				if msg.HttpTransport.Comm != tt.comm {
					t.Errorf("Expected HttpTransport.Comm '%s', got '%s'", tt.comm, msg.HttpTransport.Comm)
				}

				if msg.HttpTransport.Host != tt.host {
					t.Errorf("Expected HttpTransport.Host '%s', got '%s'", tt.host, msg.HttpTransport.Host)
				}

				// IsRequest is automatically determined by event type
				expectedIsRequest := (tt.eventType == event.EventTypeHttpRequest)
				if msg.HttpTransport.IsRequest != expectedIsRequest {
					t.Errorf("Expected HttpTransport.IsRequest %t, got %t", expectedIsRequest, msg.HttpTransport.IsRequest)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("No MCP event received")
			}
		})
	}
}

func TestRequestIDCaching_Stdio(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test request and response with matching ID
	t.Run("Request and response with matching ID", func(t *testing.T) {
		// Send a request: writer(100) -> reader(200)
		requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)
		fsEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "writer", 200, "reader")
		parser.ParseDataStdio(fsEvent)

		// Read request from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for request")
		}

		// Send a response: reader(200) -> writer(100)
		responseData := []byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`)
		fsEvent = createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		parser.ParseDataStdio(fsEvent)

		// Read response from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response")
		}
	})

	// Test response without matching request ID
	t.Run("Response without matching request ID", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		responseData := []byte(`{"jsonrpc":"2.0","id":999,"result":{"status":"ok"}}`)
		fsEvent := createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		newParser.ParseDataStdio(fsEvent)

		// Should NOT publish event due to correlation error
		select {
		case evt := <-newMockBus.Events():
			t.Errorf("Expected no event for response without matching request ID, but got event of type %v", evt.Type())
		case <-time.After(50 * time.Millisecond):
			// Success - no event published
		}
	})

	// Test request with string ID and response with matching string ID
	t.Run("Request and response with matching string ID", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		requestData := []byte(`{"jsonrpc":"2.0","id":"test-123","method":"initialize","params":{}}`)
		fsEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "writer", 200, "reader")
		newParser.ParseDataStdio(fsEvent)

		// Read request from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for request")
		}

		responseData := []byte(`{"jsonrpc":"2.0","id":"test-123","result":{"status":"ok"}}`)
		fsEvent = createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		newParser.ParseDataStdio(fsEvent)

		// Read response from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response")
		}
	})

	// Test that notifications are not affected by request ID caching
	t.Run("Notifications are not affected", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		notificationData := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)
		fsEvent := createFSAggregatedEvent(notificationData, event.EventTypeFSRead, 100, "writer", 200, "reader")
		newParser.ParseDataStdio(fsEvent)

		// Read notification from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for notification")
		}
	})

	// Test multiple requests and responses
	t.Run("Multiple requests and responses", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		// Send requests with IDs 1, 2, 3
		for i := 1; i <= 3; i++ {
			requestData := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i))
			fsEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "writer", 200, "reader")
			newParser.ParseDataStdio(fsEvent)

			// Read request from bus
			select {
			case evt := <-newMockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("No MCP event received for request %d", i)
			}
		}

		// Send response with ID 2 (should succeed)
		responseData := []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`)
		fsEvent := createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		newParser.ParseDataStdio(fsEvent)

		// Read response from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response with ID 2")
		}

		// Send response with ID 999 (should NOT publish due to correlation error)
		responseData = []byte(`{"jsonrpc":"2.0","id":999,"result":{"tools":[]}}`)
		fsEvent = createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		newParser.ParseDataStdio(fsEvent)

		// Should NOT publish event
		select {
		case evt := <-newMockBus.Events():
			t.Errorf("Expected no event for response without matching request ID, but got event of type %v", evt.Type())
		case <-time.After(50 * time.Millisecond):
			// Success - no event published
		}
	})
}

func TestRequestIDCaching_Http(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test request and response with matching ID
	t.Run("Request and response with matching ID", func(t *testing.T) {
		// Send a request first
		requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)
		httpEvent := createHttpRequestEvent(requestData, 100, "http-client", "example.com")
		parser.ParseDataHttp(httpEvent)

		// Read request from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for request")
		}

		// Send a response with matching ID
		responseData := []byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`)
		httpResponseEvent := createHttpResponseEvent(responseData, 200, "http-server", "example.com")
		parser.ParseDataHttp(httpResponseEvent)

		// Read response from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response")
		}
	})

	// Test response without matching request ID
	t.Run("Response without matching request ID", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		responseData := []byte(`{"jsonrpc":"2.0","id":999,"result":{"status":"ok"}}`)
		httpEvent := createHttpResponseEvent(responseData, 200, "http-server", "example.com")
		newParser.ParseDataHttp(httpEvent)

		// The response should be dropped (no event published)
		select {
		case evt := <-newMockBus.Events():
			t.Errorf("Expected no event (response dropped), but got event of type %v", evt.Type())
		case <-time.After(50 * time.Millisecond):
			// Success - response was dropped
		}
	})

	// Test request with string ID and response with matching string ID
	t.Run("Request and response with matching string ID", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		requestData := []byte(`{"jsonrpc":"2.0","id":"http-test-456","method":"initialize","params":{}}`)
		httpEvent := createHttpRequestEvent(requestData, 100, "http-client", "example.com")
		newParser.ParseDataHttp(httpEvent)

		// Read request from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for request")
		}

		responseData := []byte(`{"jsonrpc":"2.0","id":"http-test-456","result":{"status":"ok"}}`)
		httpResponseEvent := createHttpResponseEvent(responseData, 200, "http-server", "example.com")
		newParser.ParseDataHttp(httpResponseEvent)

		// Read response from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response")
		}
	})

	// Test that notifications are not affected by request ID caching
	t.Run("Notifications are not affected", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		notificationData := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)
		httpEvent := createSSEEvent(notificationData, 100, "http-client", "example.com")
		newParser.ParseDataHttp(httpEvent)

		// Read notification from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for notification")
		}
	})

	// Test multiple requests and responses
	t.Run("Multiple requests and responses", func(t *testing.T) {
		newMockBus := tu.NewMockBus()
		newParser, err := NewParser(newMockBus)
		if err != nil {
			t.Fatalf("Failed to create parser: %v", err)
		}
		defer newParser.Close()
		defer newMockBus.Close()

		// Send requests with IDs 1, 2, 3
		for i := 1; i <= 3; i++ {
			requestData := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i))
			httpEvent := createHttpRequestEvent(requestData, 100, "http-client", "example.com")
			newParser.ParseDataHttp(httpEvent)

			// Read request from bus
			select {
			case evt := <-newMockBus.Events():
				if evt.Type() != event.EventTypeMCPMessage {
					t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("No MCP event received for request %d", i)
			}
		}

		// Send response with ID 2 (should succeed)
		responseData := []byte(`{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`)
		httpEvent := createHttpResponseEvent(responseData, 200, "http-server", "example.com")
		newParser.ParseDataHttp(httpEvent)

		// Read response from bus
		select {
		case evt := <-newMockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response with ID 2")
		}

		// Send response with ID 999 (should be dropped)
		responseData = []byte(`{"jsonrpc":"2.0","id":999,"result":{"tools":[]}}`)
		httpEvent = createHttpResponseEvent(responseData, 200, "http-server", "example.com")
		newParser.ParseDataHttp(httpEvent)

		// Should NOT publish event
		select {
		case evt := <-newMockBus.Events():
			t.Errorf("Expected no event (response dropped), but got event of type %v", evt.Type())
		case <-time.After(50 * time.Millisecond):
			// Success - response was dropped
		}
	})
}

func TestRequestIDCaching_MixedIDTypes(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	// Test that string and int IDs don't collide
	t.Run("String and int IDs don't collide", func(t *testing.T) {
		// Send request with numeric ID 1
		requestData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		fsEvent := createFSAggregatedEvent(requestData, event.EventTypeFSRead, 100, "writer", 200, "reader")
		parser.ParseDataStdio(fsEvent)

		// Read request from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for request")
		}

		// Try response with string ID "1" (should NOT publish - different type)
		responseData := []byte(`{"jsonrpc":"2.0","id":"1","result":{"tools":[]}}`)
		fsEvent = createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		parser.ParseDataStdio(fsEvent)

		// Should NOT publish event due to correlation error
		select {
		case evt := <-mockBus.Events():
			t.Errorf("Expected no event for response with mismatched ID type, but got event of type %v", evt.Type())
		case <-time.After(50 * time.Millisecond):
			// Success - no event published
		}

		// Send response with numeric ID 1 (should succeed)
		responseData = []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
		fsEvent = createFSAggregatedEvent(responseData, event.EventTypeFSRead, 200, "reader", 100, "writer")
		parser.ParseDataStdio(fsEvent)

		// Read response from bus
		select {
		case evt := <-mockBus.Events():
			if evt.Type() != event.EventTypeMCPMessage {
				t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("No MCP event received for response")
		}
	})
}

func TestIDToCacheKey(t *testing.T) {
	parser, err := NewParser(tu.NewMockBus())
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	tests := []struct {
		name     string
		id       interface{}
		expected string
	}{
		{
			name:     "int64 ID",
			id:       int64(123),
			expected: "i:123",
		},
		{
			name:     "int ID (treated as string)",
			id:       int(456),
			expected: "s:456",
		},
		{
			name:     "string ID",
			id:       "test-789",
			expected: "s:test-789",
		},
		{
			name:     "negative int64 ID",
			id:       int64(-1),
			expected: "i:-1",
		},
		{
			name:     "empty string ID",
			id:       "",
			expected: "s:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.idToCacheKey(tt.id)
			if result != tt.expected {
				t.Errorf("Expected cache key '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestValidateResponseID(t *testing.T) {
	parser, err := NewParser(tu.NewMockBus())
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	// Cache some request IDs
	parser.cacheRequestID(int64(1))
	parser.cacheRequestID("test-123")
	parser.cacheRequestID(int64(42))

	tests := []struct {
		name     string
		id       interface{}
		expected bool
	}{
		{
			name:     "Valid int64 ID",
			id:       int64(1),
			expected: true,
		},
		{
			name:     "Valid string ID",
			id:       "test-123",
			expected: true,
		},
		{
			name:     "Valid int64 ID (42)",
			id:       int64(42),
			expected: true,
		},
		{
			name:     "Invalid ID (not cached)",
			id:       int64(999),
			expected: false,
		},
		{
			name:     "Invalid string ID (not cached)",
			id:       "not-found",
			expected: false,
		},
		{
			name:     "Nil ID",
			id:       nil,
			expected: false,
		},
		{
			name:     "String vs int mismatch",
			id:       "1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.validateResponseID(tt.id)
			if result != tt.expected {
				t.Errorf("Expected validation result %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestDuplicateDetection tests that duplicate messages (same hash) are dropped
func TestDuplicateDetection(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	// First occurrence: A(100) -> B(200)
	fsEvent := createFSAggregatedEvent(data, event.EventTypeFSRead, 100, "proc-a", 200, "proc-b")
	parser.ParseDataStdio(fsEvent)

	// Read first message from bus
	select {
	case evt := <-mockBus.Events():
		if evt.Type() != event.EventTypeMCPMessage {
			t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No MCP event received for first occurrence")
	}

	// Second occurrence (same data): B(200) -> C(300) - should be dropped as duplicate
	fsEvent = createFSAggregatedEvent(data, event.EventTypeFSRead, 200, "proc-b", 300, "proc-c")
	parser.ParseDataStdio(fsEvent)

	// Should NOT publish event (duplicate)
	select {
	case evt := <-mockBus.Events():
		t.Errorf("Expected no event (duplicate), but got event of type %v", evt.Type())
	case <-time.After(50 * time.Millisecond):
		// Success - duplicate was dropped
	}

	// Third occurrence (same data): C(300) -> D(400) - should also be dropped
	fsEvent = createFSAggregatedEvent(data, event.EventTypeFSRead, 300, "proc-c", 400, "proc-d")
	parser.ParseDataStdio(fsEvent)

	// Should NOT publish event (duplicate)
	select {
	case evt := <-mockBus.Events():
		t.Errorf("Expected no event (duplicate), but got event of type %v", evt.Type())
	case <-time.After(50 * time.Millisecond):
		// Success - duplicate was dropped
	}
}

// TestDuplicateDetection_DifferentData tests that different messages are not treated as duplicates
func TestDuplicateDetection_DifferentData(t *testing.T) {
	mockBus := tu.NewMockBus()
	parser, err := NewParser(mockBus)
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}
	defer parser.Close()
	defer mockBus.Close()

	data1 := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	data2 := []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)

	// First message: A(100) -> B(200)
	fsEvent := createFSAggregatedEvent(data1, event.EventTypeFSRead, 100, "proc-a", 200, "proc-b")
	parser.ParseDataStdio(fsEvent)

	// Read first message from bus
	select {
	case evt := <-mockBus.Events():
		if evt.Type() != event.EventTypeMCPMessage {
			t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No MCP event received for first message")
	}

	// Second message with different data: C(300) -> D(400)
	fsEvent = createFSAggregatedEvent(data2, event.EventTypeFSRead, 300, "proc-c", 400, "proc-d")
	parser.ParseDataStdio(fsEvent)

	// Should NOT be treated as duplicate (different data)
	select {
	case evt := <-mockBus.Events():
		if evt.Type() != event.EventTypeMCPMessage {
			t.Fatalf("Expected EventTypeMCPMessage, got %v", evt.Type())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("No MCP event received for second message (different data)")
	}
}
