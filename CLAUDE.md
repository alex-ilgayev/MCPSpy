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
│   └── output/          # Output formatting (console, and file output)
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
sudo ./mcpspy

# It is must be stopped by sending SIGINT (Ctrl+C) or SIGTERM.
```

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

Running solely the mcp e2e tests (without mcpspy):

```bash
make test-e2e-mcp-stdio
make test-e2e-mcp-https
```
