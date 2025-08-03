package mcp

import (
	"testing"

	"github.com/alex-ilgayev/mcpspy/pkg/ebpf"
)

func TestParseJSONRPC_ValidMessages(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name           string
		data           []byte
		expectedType   JSONRPCMessageType
		expectedMethod string
		expectedID     interface{}
		hasParams      bool
		hasResult      bool
		hasError       bool
	}{
		{
			name:           "Basic request",
			data:           []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`),
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     int64(1),
			hasParams:      true,
		},
		{
			name:           "String ID request",
			data:           []byte(`{"jsonrpc":"2.0","id":"test-123","method":"initialize","params":{"version":"1.0.0"}}`),
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "test-123",
			hasParams:      true,
		},
		{
			name:           "Request without params",
			data:           []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`),
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "tools/list",
			expectedID:     int64(2),
		},
		{
			name:         "Success response",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"OK"}]}}`),
			expectedType: JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasResult:    true,
		},
		{
			name:         "Error response",
			data:         []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Invalid params"}}`),
			expectedType: JSONRPCMessageTypeResponse,
			expectedID:   int64(1),
			hasError:     true,
		},
		{
			name:           "Notification",
			data:           []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`),
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		{
			name:           "Notification without params",
			data:           []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`),
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate write event first
			_, err := parser.ParseData(tt.data, ebpf.EventTypeFSWrite, 100, "writer")
			if err != nil {
				t.Fatalf("ParseData write failed: %v", err)
			}

			// Then read event
			msgs, err := parser.ParseData(tt.data, ebpf.EventTypeFSRead, 200, "reader")
			if err != nil {
				t.Fatalf("ParseData read failed: %v", err)
			}

			if len(msgs) != 1 {
				t.Fatalf("Expected 1 message, got %d", len(msgs))
			}

			msg := msgs[0]
			if msg.Type != tt.expectedType {
				t.Errorf("Expected type %s, got %s", tt.expectedType, msg.Type)
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
		})
	}
}

func TestParseJSONRPC_AllSupportedMethods(t *testing.T) {
	parser := NewParser()

	// Test all methods with realistic example messages based on MCP specification
	testCases := []struct {
		method         string
		messageType    string
		data           string
		expectedType   JSONRPCMessageType
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
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "init-001",
			hasParams:      true,
		},
		// Lifecycle - Server initialization with sampling capabilities
		{
			method:         "initialize",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"init-002","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"sampling":{},"roots":{"listChanged":true}},"clientInfo":{"name":"Claude-Desktop","version":"0.7.1","vendor":"Anthropic"}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "initialize",
			expectedID:     "init-002",
			hasParams:      true,
		},
		{
			method:         "ping",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"ping-123","method":"ping","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "ping",
			expectedID:     "ping-123",
			hasParams:      true,
		},
		{
			method:         "notifications/initialized",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/initialized",
			hasParams:      true,
		},
		{
			method:         "notifications/cancelled",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"tools-call-456","reason":"Operation timed out after 30 seconds"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/cancelled",
			hasParams:      true,
		},

		// Tools - Basic listing
		{
			method:         "tools/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tools-list-001","method":"tools/list","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "tools/list",
			expectedID:     "tools-list-001",
			hasParams:      true,
		},
		// Tools - File operations tool call
		{
			method:         "tools/call",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"tool-call-001","method":"tools/call","params":{"name":"filesystem_operations","arguments":{"action":"read","path":"/home/user/documents/report.md","encoding":"utf-8","max_size":1048576}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "tools/call",
			expectedID:     "tool-call-003",
			hasParams:      true,
			toolName:       "database_query",
		},
		{
			method:         "notifications/tools/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/tools/list_changed",
			hasParams:      true,
		},

		// Resources - Basic listing
		{
			method:         "resources/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resources-list-001","method":"resources/list","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "resources/list",
			expectedID:     "resources-list-001",
			hasParams:      true,
		},
		// Resources - Template listing
		{
			method:         "resources/templates/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"templates-list-001","method":"resources/templates/list","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "resources/templates/list",
			expectedID:     "templates-list-001",
			hasParams:      true,
		},
		// Resources - File system resource read
		{
			method:         "resources/read",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-read-001","method":"resources/read","params":{"uri":"file:///home/user/projects/mcp-server/config.json"}}`,
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
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
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "resources/subscribe",
			expectedID:     "resource-sub-002",
			hasParams:      true,
			resourceURI:    "webhook://api.example.com/events/user-activity",
		},
		{
			method:         "resources/unsubscribe",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"resource-unsub-001","method":"resources/unsubscribe","params":{"uri":"file:///home/user/projects/app/src/**/*.ts"}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "resources/unsubscribe",
			expectedID:     "resource-unsub-001",
			hasParams:      true,
			resourceURI:    "file:///home/user/projects/app/src/**/*.ts",
		},
		{
			method:         "notifications/resources/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/resources/list_changed","params":{}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/resources/list_changed",
			hasParams:      true,
		},
		{
			method:         "notifications/resources/updated",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///home/user/projects/app/src/main.ts","mimeType":"text/typescript","size":2048,"lastModified":"2025-01-15T14:30:00Z"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/resources/updated",
			hasParams:      true,
		},

		// Prompts - Basic listing
		{
			method:         "prompts/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompts-list-001","method":"prompts/list","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/list",
			expectedID:     "prompts-list-001",
			hasParams:      true,
		},
		// Prompts - Code review prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-001","method":"prompts/get","params":{"name":"code_review","arguments":{"language":"typescript","file":"src/components/UserProfile.tsx","focus":"security","max_suggestions":5}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-001",
			hasParams:      true,
		},
		// Prompts - Documentation generation prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-002","method":"prompts/get","params":{"name":"generate_documentation","arguments":{"codebase_path":"/home/user/projects/mcp-server","output_format":"markdown","include_examples":true,"target_audience":"developers"}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-002",
			hasParams:      true,
		},
		// Prompts - Bug analysis prompt
		{
			method:         "prompts/get",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"prompt-get-003","method":"prompts/get","params":{"name":"analyze_bug_report","arguments":{"error_message":"TypeError: Cannot read property 'id' of undefined","stack_trace":"at UserService.getUserById (user.service.ts:42:15)","reproduction_steps":"1. Login as admin\n2. Navigate to user list\n3. Click on deleted user","severity":"high"}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "prompts/get",
			expectedID:     "prompt-get-003",
			hasParams:      true,
		},
		// Completion - Code completion
		{
			method:         "completion/complete",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"completion-001","method":"completion/complete","params":{"ref":{"type":"ref","name":"typescript_completion"},"argument":{"name":"context","value":"async function processUserData(users: User[]) {\n  // Complete this function to validate and transform user data\n  "}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "completion/complete",
			expectedID:     "completion-001",
			hasParams:      true,
		},
		// Completion - SQL query completion
		{
			method:         "completion/complete",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"completion-002","method":"completion/complete","params":{"ref":{"type":"ref","name":"sql_completion"},"argument":{"name":"partial_query","value":"SELECT u.name, u.email, COUNT(o.id) as order_count FROM users u LEFT JOIN orders o ON u.id = o.user_id WHERE u.created_at > '2024-01-01' GROUP BY"}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "completion/complete",
			expectedID:     "completion-002",
			hasParams:      true,
		},
		{
			method:         "notifications/prompts/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/prompts/list_changed","params":{}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/prompts/list_changed",
			hasParams:      true,
		},

		// Progress notifications - File processing
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"file-analysis-001","progress":0.35,"total":1.0,"message":"Analyzing TypeScript files...","detail":"Processing src/components/UserProfile.tsx (142/400 files)"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		// Progress notifications - Database operation
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"db-migration-002","progress":0.82,"total":1.0,"message":"Running database migration...","detail":"Migrating table users: 82,450/100,000 records"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},
		// Progress notifications - Tool execution
		{
			method:         "notifications/progress",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"web-scrape-003","progress":0.67,"total":1.0,"message":"Scraping web pages...","detail":"Downloaded 201/300 pages, current: https://docs.example.com/api/users"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/progress",
			hasParams:      true,
		},

		// Logging - Set debug level
		{
			method:         "logging/setLevel",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"logging-001","method":"logging/setLevel","params":{"level":"debug"}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "logging/setLevel",
			expectedID:     "logging-001",
			hasParams:      true,
		},
		// Logging - Set error level
		{
			method:         "logging/setLevel",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"logging-002","method":"logging/setLevel","params":{"level":"error"}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "logging/setLevel",
			expectedID:     "logging-002",
			hasParams:      true,
		},
		// Logging - Info message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"MCP server initialized successfully with 15 tools and 42 resources","logger":"mcp-filesystem-server","timestamp":"2025-01-15T14:30:15.123Z"}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},
		// Logging - Error message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"error","data":"Failed to connect to database: connection timeout after 30s","logger":"mcp-database-server","timestamp":"2025-01-15T14:30:45.567Z","extra":{"host":"localhost:5432","database":"production","retry_count":3}}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},
		// Logging - Debug message
		{
			method:         "notifications/message",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"debug","data":"Tool execution completed","logger":"mcp-tools-server","timestamp":"2025-01-15T14:30:50.890Z","extra":{"tool_name":"web_search","execution_time_ms":1547,"result_size":2048,"cache_hit":false}}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/message",
			hasParams:      true,
		},

		// Client capabilities - Sampling with code analysis
		{
			method:         "sampling/createMessage",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"sampling-001","method":"sampling/createMessage","params":{"messages":[{"role":"user","content":{"type":"text","text":"Please analyze this TypeScript code for potential security vulnerabilities and suggest improvements"}},{"role":"user","content":{"type":"resource","resource":{"uri":"file:///home/user/project/src/auth.ts","mimeType":"text/typescript"}}}],"modelPreferences":{"costPriority":0.8,"speedPriority":0.2,"intelligencePriority":0.9},"systemPrompt":"You are a senior security engineer reviewing code for production deployment."}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "sampling/createMessage",
			expectedID:     "sampling-001",
			hasParams:      true,
		},
		// Client capabilities - Sampling with conversation context
		{
			method:         "sampling/createMessage",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"sampling-002","method":"sampling/createMessage","params":{"messages":[{"role":"user","content":{"type":"text","text":"I need help debugging this error"}},{"role":"assistant","content":{"type":"text","text":"I'd be happy to help! Could you share the error message and relevant code?"}},{"role":"user","content":{"type":"text","text":"Here's the error: Cannot read property 'id' of undefined. The code is in UserService.getUserById method."}}],"modelPreferences":{"hints":[{"name":"temperature","value":0.3},{"name":"max_tokens","value":1000}]},"includeContext":"conversation"}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "sampling/createMessage",
			expectedID:     "sampling-002",
			hasParams:      true,
		},
		// Client capabilities - User input elicitation for API keys
		{
			method:         "elicitation/create",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"elicit-001","method":"elicitation/create","params":{"prompt":"Please provide your GitHub API token to access private repositories","inputType":"password","placeholder":"ghp_xxxxxxxxxxxxxxxxxxxx","validation":{"pattern":"^ghp_[A-Za-z0-9_]{36}$","errorMessage":"Invalid GitHub token format"}}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "elicitation/create",
			expectedID:     "elicit-001",
			hasParams:      true,
		},
		// Client capabilities - User input elicitation for configuration
		{
			method:         "elicitation/create",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"elicit-002","method":"elicitation/create","params":{"prompt":"Enter the database connection string for your development environment","inputType":"text","placeholder":"postgresql://username:password@localhost:5432/dbname","required":true,"description":"This will be used to connect to your local PostgreSQL database for development."}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "elicitation/create",
			expectedID:     "elicit-002",
			hasParams:      true,
		},
		// Client capabilities - Roots listing
		{
			method:         "roots/list",
			messageType:    "request",
			data:           `{"jsonrpc":"2.0","id":"roots-list-001","method":"roots/list","params":{}}`,
			expectedType:   JSONRPCMessageTypeRequest,
			expectedMethod: "roots/list",
			expectedID:     "roots-list-001",
			hasParams:      true,
		},
		{
			method:         "notifications/roots/list_changed",
			messageType:    "notification",
			data:           `{"jsonrpc":"2.0","method":"notifications/roots/list_changed","params":{"added":[{"uri":"file:///home/user/new-project","name":"New Project"}],"removed":["file:///home/user/old-project"]}}`,
			expectedType:   JSONRPCMessageTypeNotification,
			expectedMethod: "notifications/roots/list_changed",
			hasParams:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.method, func(t *testing.T) {
			data := []byte(tc.data)

			// Write then read
			_, err := parser.ParseData(data, ebpf.EventTypeFSWrite, 100, "writer")
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			msgs, err := parser.ParseData(data, ebpf.EventTypeFSRead, 200, "reader")
			if err != nil {
				t.Fatalf("Read failed for method %s: %v", tc.method, err)
			}

			if len(msgs) != 1 {
				t.Fatalf("Expected 1 message, got %d", len(msgs))
			}

			msg := msgs[0]

			// Validate message type
			if msg.Type != tc.expectedType {
				t.Errorf("Expected type %s, got %s", tc.expectedType, msg.Type)
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
		})
	}
}

func TestParseJSONRPC_InvalidMessages(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		data        []byte
		expectError string
	}{
		{
			name:        "Invalid JSON",
			data:        []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"`),
			expectError: "invalid JSON",
		},
		{
			name:        "Missing jsonrpc field",
			data:        []byte(`{"id":1,"method":"tools/call"}`),
			expectError: "invalid JSON-RPC version",
		},
		{
			name:        "Wrong jsonrpc version",
			data:        []byte(`{"jsonrpc":"1.0","id":1,"method":"tools/call"}`),
			expectError: "invalid JSON-RPC version",
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
			// Write event
			_, err := parser.ParseData(tt.data, ebpf.EventTypeFSWrite, 100, "writer")
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			// Read event should fail
			_, err = parser.ParseData(tt.data, ebpf.EventTypeFSRead, 200, "reader")
			if err == nil {
				t.Errorf("Expected error containing '%s', got nil", tt.expectError)
				return
			}

			if len(tt.expectError) > 0 && err.Error()[:len(tt.expectError)] != tt.expectError {
				t.Errorf("Expected error containing '%s', got '%s'", tt.expectError, err.Error())
			}
		})
	}
}

func TestParseData_WriteReadCorrelation(t *testing.T) {
	parser := NewParser()

	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`)

	// Test normal flow: write then read
	t.Run("Normal flow", func(t *testing.T) {
		// Write event
		writeMsg, err := parser.ParseData(data, ebpf.EventTypeFSWrite, 100, "writer")
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if len(writeMsg) != 0 {
			t.Errorf("Expected no messages from write event, got %d", len(writeMsg))
		}

		// Read event
		readMsg, err := parser.ParseData(data, ebpf.EventTypeFSRead, 200, "reader")
		if err != nil {
			t.Fatalf("Read failed: %v", err)
		}
		if len(readMsg) != 1 {
			t.Fatalf("Expected 1 message from read event, got %d", len(readMsg))
		}

		msg := readMsg[0]
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
	})

	// Test read without write
	t.Run("Read without write", func(t *testing.T) {
		newParser := NewParser()
		data2 := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

		_, err := newParser.ParseData(data2, ebpf.EventTypeFSRead, 200, "reader")
		if err == nil {
			t.Error("Expected error for read without write")
		}
		if err.Error() != "no write event found for the parsed read event" {
			t.Errorf("Expected specific error, got: %s", err.Error())
		}
	})
}

func TestParseData_MultipleMessages(t *testing.T) {
	parser := NewParser()

	// Test multiple JSON messages separated by newlines
	multipleData := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","method":"notifications/progress","params":{"value":50}}`)

	// Write event
	_, err := parser.ParseData(multipleData, ebpf.EventTypeFSWrite, 100, "writer")
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read event
	msgs, err := parser.ParseData(multipleData, ebpf.EventTypeFSRead, 200, "reader")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(msgs) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(msgs))
	}

	// Verify message types
	expectedTypes := []JSONRPCMessageType{
		JSONRPCMessageTypeRequest,
		JSONRPCMessageTypeRequest,
		JSONRPCMessageTypeNotification,
	}

	for i, expectedType := range expectedTypes {
		if msgs[i].Type != expectedType {
			t.Errorf("Message %d: expected type %s, got %s", i, expectedType, msgs[i].Type)
		}
	}
}

func TestExtractToolName(t *testing.T) {
	tests := []struct {
		name     string
		msg      *Message
		expected string
	}{
		{
			name: "Valid tool call",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
					Method: "tools/list",
				},
			},
			expected: "",
		},
		{
			name: "No params",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
					Method: "tools/call",
				},
			},
			expected: "",
		},
		{
			name: "Non-string name",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
		msg      *Message
		expected string
	}{
		{
			name: "Valid resource read",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
					Method: "tools/call",
				},
			},
			expected: "",
		},
		{
			name: "No params",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
					Method: "resources/read",
				},
			},
			expected: "",
		},
		{
			name: "Non-string URI",
			msg: &Message{
				JSONRPCMessage: JSONRPCMessage{
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
	parser := NewParser()
	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	// Test unsupported event type
	_, err := parser.ParseData(data, ebpf.EventType(99), 100, "test")
	if err == nil {
		t.Error("Expected error for unsupported event type")
	}
	if err.Error() != "unknown event type: 99" {
		t.Errorf("Expected unknown event type error, got: %s", err.Error())
	}
}
