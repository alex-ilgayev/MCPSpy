# MCPSpy - MCP Monitoring with eBPF 🕵️✨

[![Run in Smithery](https://smithery.ai/badge/skills/alex-ilgayev)](https://smithery.ai/skills?ns=alex-ilgayev&utm_source=github&utm_medium=badge)


[![CI](https://github.com/alex-ilgayev/mcpspy/actions/workflows/ci.yml/badge.svg)](https://github.com/alex-ilgayev/mcpspy/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.24+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

<div align="center">
<pre>
███╗   ███╗ ██████╗██████╗ ███████╗██████╗ ██╗   ██╗
████╗ ████║██╔════╝██╔══██╗██╔════╝██╔══██╗╚██╗ ██╔╝
██╔████╔██║██║     ██████╔╝███████╗██████╔╝ ╚████╔╝ 
██║╚██╔╝██║██║     ██╔═══╝ ╚════██║██╔═══╝   ╚██╔╝  
██║ ╚═╝ ██║╚██████╗██║     ███████║██║        ██║   
╚═╝     ╚═╝ ╚═════╝╚═╝     ╚══════╝╚═╝        ╚═╝   
</pre>
<b>MCPSpy - Real-time monitoring for Model Context Protocol communication using eBPF</b>
</div>

## Overview

MCPSpy is a powerful command-line tool that leverages [eBPF (Extended Berkeley Packet Filter)](https://ebpf.io/) technology to monitor [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) communication at the kernel level. It provides real-time visibility into JSON-RPC 2.0 messages exchanged between MCP clients and servers by hooking into low-level system calls.

The Model Context Protocol supports three transport protocols for communication:

- **Stdio**: Communication over standard input/output streams
- **Streamable HTTP**: Direct HTTP request/response communication with server-sent events
- **SSE (Server-Sent Events)**: HTTP-based streaming communication (_Deprecated_)

**MCPSpy supports monitoring of both Stdio and HTTP/HTTPS transports** (including Server-Sent Events), providing comprehensive coverage of MCP communication channels.

![demo](./assets/demo.gif)

## Why MCPSpy?

The Model Context Protocol is becoming the standard for AI tool integration, but understanding what's happening under the hood can be challenging. MCPSpy addresses this by providing:

- **🔒 Security Analysis**: Monitor what data is being transmitted, detect PII leakage, and audit tool executions
- **🛡️ Prompt Injection Detection**: Real-time detection of prompt injection and jailbreak attempts using ML models
- **🐛 Debugging**: Troubleshoot MCP integrations by seeing the actual message flow
- **📊 Performance Monitoring**: Track message patterns and identify bottlenecks
- **🔍 Compliance**: Ensure MCP communications meet regulatory requirements
- **🎓 Learning**: Understand how MCP works by observing real communications

## Installation

### Prerequisites

- Linux kernel version 5.15 or later
- Root privileges (required for eBPF)

### Download Pre-built Binary (Auto-detect OS + Arch)

Download the latest release from the [release page](https://github.com/alex-ilgayev/mcpspy/releases):

```bash
# Set platform-aware binary name
BIN="mcpspy-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"

# Download the correct binary
wget "https://github.com/alex-ilgayev/mcpspy/releases/latest/download/${BIN}"

# Make it executable and move to a directory in your PATH
chmod +x "${BIN}"
sudo mv "${BIN}" /usr/local/bin/mcpspy
```

> ✅ Note: Currently supported platforms: linux-amd64, linux-arm64

### Build from Source

#### Install Dependencies

First, install the required system dependencies:

```bash
sudo apt-get update
# Install build essentials, eBPF dependencies
sudo apt-get install -y clang clang-format llvm make libbpf-dev build-essential
# Install Python 3 and pip (for e2e tests)
sudo apt-get install -y python3 python3-pip python3-venv
# Install docker and buildx (if not already installed)
sudo apt-get install -y docker.io docker-buildx
```

#### Install Go

MCPSpy requires Go 1.24 or later. Install Go using one of these methods:

Option 1: Install from the official Go website (Recommended)

```bash
# Download and install Go 1.24.1 (adjust version as needed)
wget https://go.dev/dl/go1.24.1.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.1.linux-amd64.tar.gz

# Add Go to PATH (add this to your ~/.bashrc or ~/.profile for persistence)
export PATH=$PATH:/usr/local/go/bin
```

Option 2: Install via snap

```bash
sudo snap install go --classic
```

#### Build MCPSpy

Clone the repository and build MCPSpy:

```bash
# Clone the repository
git clone https://github.com/alex-ilgayev/mcpspy.git
cd mcpspy

# Build the project
make all
```

### Docker

```bash
# Build Docker image
make image
# Or pull the latest image
docker pull ghcr.io/alex-ilgayev/mcpspy:latest
# Or pull a specific image release
docker pull ghcr.io/alex-ilgayev/mcpspy:v0.1.0

# Run the container
docker run --rm -it --privileged ghcr.io/alex-ilgayev/mcpspy:latest
```

### Kubernetes

MCPSpy can be deployed in Kubernetes clusters to monitor MCP traffic from AI/LLM services like LangFlow, LangGraph, and other applications that use the Model Context Protocol.

```bash
# Deploy MCPSpy as a DaemonSet
kubectl apply -f https://raw.githubusercontent.com/alex-ilgayev/mcpspy/main/deploy/kubernetes/mcpspy.yaml
```

#### Real-World Use Cases in Kubernetes

1. **Monitoring LangFlow/LangGraph Deployments**

   - Observe MCP traffic between LangFlow/LangGraph and AI services
   - Debug integration issues in complex AI workflows
   - Audit AI interactions for security and compliance

2. **AI Service Monitoring**

   - Track interactions with both remote and local MCP servers
   - Identify performance bottlenecks in AI service calls
   - Detect potential data leakage in AI communications

3. **Development and Testing**
   - Test MCP implementations in containerized environments
   - Validate AI service integrations before production deployment
   - Ensure consistent behavior across different environments

For detailed instructions and real-world examples of monitoring AI services in Kubernetes, see the [Kubernetes Usage Guide](docs/kubernetes-usage.md).

## Usage

### Basic Usage

```bash
# Start monitoring MCP communication (TUI mode is default)
sudo mcpspy

# Start monitoring with static console output (disable TUI)
sudo mcpspy --tui=false

# Start monitoring and save output to JSONL file
sudo mcpspy -o output.jsonl

# Stop monitoring with Ctrl+C (or 'q' in TUI mode)
```

### Prompt Injection Detection

MCPSpy includes optional real-time prompt injection detection using HuggingFace's Inference API. When enabled, it analyzes MCP tool calls for potential injection attacks and jailbreak attempts.

**Detection coverage:**

1. **Request-based injection**: Detects malicious prompts in tool call arguments
2. **Response-based injection**: Detects malicious content in tool responses that could manipulate the agent

```bash
# Enable security scanning with HuggingFace token
sudo mcpspy --security --hf-token=hf_xxxxx

# Use a custom detection model
sudo mcpspy --security --hf-token=hf_xxxxx --security-model=protectai/deberta-v3-base-prompt-injection-v2

# Adjust detection threshold (default: 0.5)
sudo mcpspy --security --hf-token=hf_xxxxx --security-threshold=0.7

# Run analysis synchronously (blocks until analysis completes)
sudo mcpspy --security --hf-token=hf_xxxxx --security-async=false
```

**Security CLI Flags:**

| Flag                   | Description                                               | Default                               |
| ---------------------- | --------------------------------------------------------- | ------------------------------------- |
| `--security`           | Enable prompt injection detection                         | `false`                               |
| `--hf-token`           | HuggingFace API token (required when security is enabled) | -                                     |
| `--security-model`     | HuggingFace model for detection                           | `meta-llama/Llama-Prompt-Guard-2-86M` |
| `--security-threshold` | Detection threshold (0.0-1.0)                             | `0.5`                                 |
| `--security-async`     | Run analysis asynchronously                               | `true`                                |

**Supported Models:**

- `meta-llama/Llama-Prompt-Guard-2-86M` (default, requires HF license acceptance)
- `protectai/deberta-v3-base-prompt-injection-v2` (publicly accessible)

When a potential injection is detected, MCPSpy displays a security alert with risk level (low/medium/high/critical), category, and the analyzed content.

### Output Format

#### TUI Mode (Default)

MCPSpy runs in interactive Terminal UI mode by default. The TUI provides:

- Interactive table view with scrolling
- Detailed message inspection (press Enter)
- Filtering by transport, type, and actor
- Multiple density modes for different screen sizes
- Real-time statistics

#### Static Console Output

When running with `--tui=false`:

```

12:34:56.789 python[12345] → python[12346] REQ tools/call (get_weather) Execute a tool
12:34:56.890 python[12346] → python[12345] RESP OK

```

#### JSONL Output

**Stdio Transport - Request:**

```json
{
  "timestamp": "2024-01-15T12:34:56.789Z",
  "transport_type": "stdio",
  "stdio_transport": {
    "from_pid": 12345,
    "from_comm": "python",
    "to_pid": 12346,
    "to_comm": "python"
  },
  "type": "request",
  "id": 7,
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": { "city": "New York" }
  },
  "error": {},
  "raw": "{...}"
}
```

**Stdio Transport - Response:**

```json
{
  "timestamp": "2024-01-15T12:34:56.890Z",
  "transport_type": "stdio",
  "stdio_transport": {
    "from_pid": 12346,
    "from_comm": "python",
    "to_pid": 12345,
    "to_comm": "python"
  },
  "type": "response",
  "id": 7,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Weather in New York: 20°C"
      }
    ],
    "isError": false
  },
  "error": {},
  "request": {
    "type": "request",
    "id": 7,
    "method": "tools/call",
    "params": {
      "name": "get_weather",
      "arguments": { "city": "New York" }
    },
    "error": {}
  },
  "raw": "{...}"
}
```

**HTTP/HTTPS Transport - Request:**

```json
{
  "timestamp": "2024-01-15T12:34:56.789Z",
  "transport_type": "http",
  "http_transport": {
    "pid": 47837,
    "comm": "python",
    "host": "127.0.0.1:12345",
    "is_request": true
  },
  "type": "request",
  "id": 7,
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": { "city": "New York" }
  },
  "error": {},
  "raw": "{...}"
}
```

**HTTP/HTTPS Transport - Response:**

```json
{
  "timestamp": "2024-01-15T12:34:56.890Z",
  "transport_type": "http",
  "http_transport": {
    "pid": 47837,
    "comm": "python",
    "host": "127.0.0.1:12345"
  },
  "type": "response",
  "id": 7,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Weather in New York: 20°C"
      }
    ],
    "isError": false
  },
  "error": {},
  "request": {
    "type": "request",
    "id": 7,
    "method": "tools/call",
    "params": {
      "name": "get_weather",
      "arguments": { "city": "New York" }
    },
    "error": {}
  },
  "raw": "{...}"
}
```

## Architecture

MCPSpy uses an event-driven architecture with a publish-subscribe pattern to decouple components and enable extensibility. The system consists of several components that communicate through a central event bus:

### 1. Event Bus (`pkg/bus/`)

- Central communication hub using publish-subscribe pattern
- Enables asynchronous event processing
- Using `github.com/asaskevich/EventBus` library

### 2. eBPF Program (`bpf/`)

- Hooks into `vfs_read` and `vfs_write` kernel functions for stdio transport
- Hooks into TLS library functions (`SSL_read`, `SSL_write`) for HTTP/HTTPS transport
- Filters potential MCP traffic by detecting JSON patterns
- Sends events to userspace via ring buffer
- Minimal performance impact with early filtering

### 3. eBPF Loader (`pkg/ebpf/`)

- Manages the lifecycle of eBPF programs and resources
- Loads pre-compiled eBPF objects into the kernel using cilium/ebpf library
- Converts raw binary events from kernel space into structured Go data types
- Publishes events to the event bus for downstream processing

### 4. HTTP Session Manager (`pkg/http/`)

- Subscribes to TLS-related events from the event bus
- Manages HTTP/HTTPS sessions and correlates request/response pairs
- Handles TLS payload interception and parsing
- Supports chunked transfer encoding and Server-Sent Events (SSE)
- Reconstructs complete HTTP messages from fragmented TLS data
- Publishes reconstructed HTTP bodies to the event bus for MCP parsing

### 5. MCP Protocol Parser (`pkg/mcp/`)

- Subscribes to data events from the event bus (stdio and HTTP TLS payloads)
- Validates JSON-RPC 2.0 message format
- Parses MCP-specific methods and parameters
- Correlates read operations and write operations into a single MCP message (relevant for stdio transport)
- Supports both stdio and HTTP/HTTPS transports (including SSE)
- Publishes parsed MCP messages to the event bus

### 6. Output Handlers (`pkg/output/`)

- Subscribe to MCP message events from the event bus
- Console display with colored, formatted output
- JSONL output for programmatic analysis
- Real-time statistics tracking

### 7. Event Logger (`pkg/eventlogger/`)

- Subscribes to all events on the event bus for debugging
- Provides detailed logging of event flow through the system
- Configurable log levels for different event types

### 8. Security Analyzer (`pkg/security/`)

- Optional component for real-time prompt injection detection
- Subscribes to MCP message events from the event bus
- Analyzes high-risk methods (`tools/call`, `resources/read`, `prompts/get`)
- Uses HuggingFace Inference API with configurable ML models
- Publishes security alerts when injections are detected
- Supports async and sync analysis modes

## Development

### Building

```bash
# Generate eBPF bindings and build
make all

# Build Docker image
make image
```

### Testing

MCPSpy includes comprehensive end-to-end tests that simulate real MCP communication across different transports:

```bash
# (Optional) Set up test environment
make test-e2e-setup

# Run all tests (requires root privileges)
make test-e2e

# Run individual transport tests
make test-e2e-stdio   # Test stdio transport
make test-e2e-https   # Test HTTP/HTTPS transport
```

The test suite includes:

- MCP server and client simulators for both stdio and HTTP transports
- Message validation against expected outputs
- Multiple message type coverage
- SSL/TLS encrypted HTTP communication testing

## Limitations

- **FS Events Buffer Size**: Limited to 16KB per message. This means MCP messages with **buffer size** greater than 16KB will be missed / ignored.
- **FS Events Constructed of Multiple Messages**: MCPSpy currently does not support reconstructing MCP messages that are split across multiple `read` or `write` syscalls. This means that if an MCP message is larger than the buffer size used in a single syscall, it may be missed or ignored.
- **Inode Collision for Stdio Transport**: Inode numbers are only unique within a filesystem. If monitoring processes across multiple filesystems or mount namespaces, inode collisions are theoretically possible but rare in practice for pipe-based stdio communication.
- **Platform**: Linux only (kernel 5.15+).

## Contributing

We welcome contributions! Feel free to open an issue or a pull request.

## License

- **User-mode code** (Mainly Go): Apache 2.0 (see [LICENSE](LICENSE))
- **eBPF C programs** (`bpf/*`): GPL-2.0-only (see [LICENSE-BPF](LICENSE-BPF))

---

<div align="center">
Made with ❤️ by Alex Ilgayev
</div>
