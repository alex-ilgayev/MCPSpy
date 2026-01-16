# MCPSpy Project Rules

## Project Overview

MCPSpy is a CLI utility that uses eBPF to monitor MCP (Model Context Protocol) communication by tracking stdio operations and analyzing JSON-RPC 2.0 messages.

## Technology Stack

- Go 1.24+ for the main application
- C for eBPF programs
- cilium/ebpf for loading eBPF programs
- Docker for containerization

## Project Structure

```
mcpspy/
├── cmd/mcpspy/          # CLI entry point
├── pkg/
│   ├── bus/             # Event bus for publish-subscribe communication
│   ├── ebpf/            # eBPF loading and management
│   ├── event/           # Event definitions and handling
│   ├── eventlogger/     # Event logging component
│   ├── fs/              # Filesystem (stdio) session management and JSON aggregation
│   ├── http/            # HTTP transport parsing and analysis
│   ├── mcp/             # MCP protocol parsing and analysis
│   ├── output/          # Output formatting (console, and file output)
│   └── security/        # Prompt injection detection via HuggingFace API
│       ├── hf/          # HuggingFace Inference API client
│       └── testdata/    # Test samples for security integration tests
├── internal/
│   └── testing/         # Testing utilities (mock event bus, etc.)
├── bpf/                 # eBPF C programs
├── tests/               # Test files
├── deploy/docker/       # Docker configuration
└── .github/workflows/   # CI/CD workflows
```

## Architecture

MCPSpy uses an event-driven architecture with a publish-subscribe pattern:

- **Event Bus (`pkg/bus/`)**: Central communication hub that decouples components
- Components communicate by publishing and subscribing to typed events
- The eBPF loader publishes raw events from kernel space
- The HTTP session manager and MCP parser subscribe to relevant events
- Output handlers subscribe to parsed MCP messages

## MCP Protocol Context

- MCP uses JSON-RPC 2.0 format
- Messages have jsonrpc: "2.0" field
- Three message types: Request, Response, Notification
- Track method names like "tools/call", "resources/read", etc.
- Focus on both stdio and HTTP transport (streamable HTTP).

## Performance Guidelines

- Use efficient data structures
- Minimize data copying between kernel and userspace
- Filter early in eBPF to reduce overhead
- Event bus uses asynchronous processing to avoid blocking

## Building

```bash
make build
```

## Running

```bash
# Run with TUI mode (default)
sudo ./mcpspy

# Run with static console output (for testing/debugging)
sudo ./mcpspy --tui=false

# It is must be stopped by sending SIGINT (Ctrl+C) or SIGTERM.
# In TUI mode, you can also press 'q' to quit.
```

## Debug Mode

Debug mode allows inspecting raw eBPF events and derived events for troubleshooting parsing issues, developing new protocol support, or understanding the event flow.

```bash
# Show all events
sudo ./mcpspy debug

# Show only MCP messages with payloads
sudo ./mcpspy debug --events mcp_message --payload

# Filter by PID
sudo ./mcpspy debug --pid 12345

# Filter by process name
sudo ./mcpspy debug --comm claude

# Debug HTTP parsing issues
sudo ./mcpspy debug --events tls_recv,http_request,http_response --payload

# Show HTTP headers
sudo ./mcpspy debug --events http_request,http_response --headers

# Combine filters with headers
sudo ./mcpspy debug --events http_request,http_response --pid 12345 --headers --payload
```

**Available event types:**
- Raw eBPF: `fs_read`, `fs_write`, `library`, `tls_send`, `tls_recv`, `tls_free`
- Derived: `http_request`, `http_response`, `http_sse`, `mcp_message`, `fs_aggregated_read`, `fs_aggregated_write`, `security_alert`, `llm_message`, `tool_usage`

**Flags:**
- `--events, -e`: Filter by event type(s), comma-separated
- `--pid, -p`: Filter by specific PID
- `--comm, -c`: Filter by process name (substring match)
- `--host`: Filter by URL regex (matches against host+path, e.g., `api\.anthropic\.com/v1/messages`)
- `--payload`: Show payload/buffer data for events
- `--headers`: Show HTTP headers for HTTP events

## Testing

Running all tests (unit tests, and end-to-end tests for both stdio and https transports):

```bash
make test
```

Running unit tests:

```bash
make test-unit
```

Running end-to-end tests (including mcpspy, and verifying the output):

```bash
make test-e2e-stdio
make test-e2e-https

# Or
make test-e2e
```

Running scenario tests (without mcpspy):

```bash
make test-scenario-stdio
make test-scenario-https
make test-scenario-security  # Security/injection test
```

**Note:** E2E tests automatically run MCPSpy in static mode (`--tui=false`) to capture output for validation. The test configuration is in `tests/e2e_config.yaml`.

## Integration Tests

Integration tests verify functionality with real external services. Currently includes security/prompt injection detection tests with HuggingFace API:

```bash
# Run integration tests (requires HF_TOKEN)
HF_TOKEN=hf_xxx make test-integration

# Override the model (default: protectai/deberta-v3-base-prompt-injection-v2)
HF_TOKEN=hf_xxx HF_MODEL=your-model-name make test-integration
```

**Test structure:**

- `pkg/security/testdata/samples.json` - Test samples (benign and malicious)
- `pkg/security/hf/integration_test.go` - HF client integration tests
- `pkg/security/integration_test.go` - Full analyzer integration tests with event bus

**Notes:**

- Integration tests use build tag `//go:build integration`
- Default model is `protectai/deberta-v3-base-prompt-injection-v2` (non-gated)
- Unit tests run without API calls via mock HTTP server

## Git Worktrees for Parallel Claude Sessions

Use worktrees to run multiple Claude sessions on separate feature branches.
Use the .worktrees/ directory to keep things organized in terms of permissions:

```bash
# Create worktree with new branch
git worktree add .worktrees/MCPSpy-<feature> -b feat/<feature-name>

# Work in the new directory
cd .worktrees/MCPSpy-<feature> && claude

# Cleanup after merge
git worktree remove .worktrees/MCPSpy-<feature>
```
