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

- Linux kernel version 5.15 or later
- Root privileges (required for eBPF)

### Download Pre-built Binary (Auto-detect OS + Arch)

Download the latest release from the [releases page](https://github.com/alex-ilgayev/mcpspy/releases):

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
make all

# Build Docker image
make image
```

### Testing

MCPSpy includes comprehensive end-to-end tests that simulate real MCP communication:

```bash
# (Optional) Set up test environment
make test-e2e-setup

# Run tests (requires root privileges)
make test-e2e
```

The test suite includes:

- MCP server and client simulators
- Message validation against expected outputs
- Multiple message type coverage

## Limitations

- **FS Events Buffer Size**: Limited to 16KB per message. This means MCP messages larger than 16KB will be missed / ignored.
- **Platform**: Linux only (kernel 5.15+).
- **Transport**: Currently supports stdio transport only. Support for streamable HTTP and SSE transports is planned.

## Contributing

We welcome contributions! Feel free to open an issue or a pull request.

## License

- **User-mode code** (Mainly Go): Apache 2.0 (see [LICENSE](LICENSE))
- **eBPF C programs** (`bpf/*`): GPL-2.0-only (see [LICENSE-BPF](LICENSE-BPF))

---

<div align="center">
Made with ❤️ by Alex Ilgayev
</div>
