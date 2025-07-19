# MCPSpy - MCP Monitoring with eBPF 🕵️✨

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

**MCPSpy currently supports only Stdio transport monitoring**, with plans to extend support to SSE and HTTP transports in future releases.

![demo](./assets/demo.gif)

## Why MCPSpy?

The Model Context Protocol is becoming the standard for AI tool integration, but understanding what's happening under the hood can be challenging. MCPSpy addresses this by providing:

- **🔒 Security Analysis**: Monitor what data is being transmitted, detect PII leakage, and audit tool executions
- **🐛 Debugging**: Troubleshoot MCP integrations by seeing the actual message flow
- **📊 Performance Monitoring**: Track message patterns and identify bottlenecks
- **🔍 Compliance**: Ensure MCP communications meet regulatory requirements
- **🎓 Learning**: Understand how MCP works by observing real communications

## Installation

### Prerequisites

- Linux kernel version 5.10 or later
- Root privileges (required for eBPF)

### Download Pre-built Binary

Download the latest release from the [releases page](https://github.com/alex-ilgayev/mcpspy/releases):

```bash
wget https://github.com/alex-ilgayev/mcpspy/releases/latest/download/mcpspy
chmod +x mcpspy
sudo mv mcpspy /usr/local/bin/
```

### Build from Source

```bash
git clone https://github.com/alex-ilgayev/mcpspy.git
cd mcpspy

sudo apt-get update
sudo apt-get install -y clang llvm make libbpf-dev
make
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

## Usage

### Basic Usage

```bash
# Start monitoring MCP communication
sudo mcpspy

# Start monitoring with raw message buffers
sudo mcpspy -b

# Start monitoring and save output to JSONL file
sudo mcpspy -o output.jsonl

# Stop monitoring with Ctrl+C
```

### Output Format

#### Console Output

```

12:34:56.789 python[12345] → python[12346] REQ tools/call (get_weather) Execute a tool
12:34:56.890 python[12346] → python[12345] RESP OK

```

#### JSONL Output

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
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": { "city": "New York" }
  }
}
```

## Architecture

MCPSpy consists of several components:

### 1. eBPF Program (`bpf/`)

- Hooks into `vfs_read` and `vfs_write` kernel functions
- Filters potential MCP traffic by detecting JSON patterns
- Sends events to userspace via ring buffer
- Minimal performance impact with early filtering

### 2. eBPF Loader (`pkg/ebpf/`)

- Manages the lifecycle of eBPF programs and resources
- Loads pre-compiled eBPF objects into the kernel using cilium/ebpf library
- Converts raw binary events from kernel space into structured Go data types

### 3. MCP Protocol Parser (`pkg/mcp/`)

- Validates JSON-RPC 2.0 message format
- Parses MCP-specific methods and parameters
- Correlates read operations and write operations into a single MCP message (relevant for stdio transport)
- Currently supports stdio transport (streamable HTTP/SSE planned)

### 4. Output Handlers (`pkg/output/`)

- Console display with colored, formatted output
- JSONL output for programmatic analysis
- Real-time statistics tracking

## Development

### Building

```bash
# Generate eBPF bindings and build
make

# Build Docker image
make image
```

### Testing

MCPSpy includes comprehensive end-to-end tests that simulate real MCP communication:

```bash
# (Optional) Set up test environment
make test-setup

# Run tests (requires root privileges)
make test-e2e
```

The test suite includes:

- MCP server and client simulators
- Message validation against expected outputs
- Multiple message type coverage

## Limitations

- **FS Events Buffer Size**: Limited to 16KB per message. This means MCP messages larger than 16KB will be missed / ignored.
- **Platform**: Linux only (kernel 5.10+).
- **Transport**: Currently supports stdio transport only. Support for streamable HTTP and SSE transports is planned.

## Roadmap

- [ ] Support for streamable HTTP and SSE transports.
- [ ] Response validation with request correlation.
- [ ] Extended message data extraction (MCP payloads).
- [ ] Add e2e tests for additional MCP methods.
- [ ] Release flow for multiple platforms. (e.g., arm64, etc.)

## Contributing

We welcome contributions! Feel free to open an issue or a pull request.

## License

- **User-mode code** (Mainly Go): Apache 2.0 (see [LICENSE](LICENSE))
- **eBPF C programs** (`bpf/*`): GPL-2.0-only (see [LICENSE-BPF](LICENSE-BPF))

---

<div align="center">
Made with ❤️ by Alex Ilgayev
</div>
