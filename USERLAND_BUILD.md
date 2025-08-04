# Userland Mode Build Instructions

## Overview
The userland mode provides an alternative to eBPF monitoring using LD_PRELOAD library injection. This mode is useful for environments where eBPF is not available or when monitoring stdio-based MCP communication is sufficient.

## Build Dependencies

### Required Tools
- Go 1.20 or later
- GCC compiler
- Make

### Build Process

1. **Build the userland C library:**
```bash
cd userland
make clean
make
```

This creates `libmcpspy.so` which contains the LD_PRELOAD hooks for intercepting system calls.

2. **Build the Go binary with userland support:**
```bash
go build -tags userland -o mcpspy-userland ./cmd/mcpspy
```

3. **Copy the library to the binary location:**
```bash
cp userland/libmcpspy.so .
```

## Usage

Run MCPSpy in userland mode:
```bash
./mcpspy-userland --mode userland [command]
```

The userland mode will:
- Set LD_PRELOAD to inject libmcpspy.so into the target process
- Monitor read() and write() system calls on stdio (fd 0, 1, 2)
- Detect and parse JSON-RPC 2.0 messages
- Log MCP communication in JSONL format

## Limitations

Current userland implementation only supports:
- stdio transport (stdin/stdout/stderr)
- Basic read/write system call interception
- Linux and macOS platforms

Network transport monitoring (HTTP, SSL, sockets) has been removed from this initial implementation to keep it simple and focused.

## Differences from eBPF Mode

| Feature | eBPF Mode | Userland Mode |
|---------|-----------|---------------|
| Kernel dependency | Linux kernel with eBPF | None |
| Root privileges | Required | Not required |
| Performance overhead | Minimal | Slightly higher |
| Transport support | stdio | stdio only |
| Platform support | Linux only | Linux, macOS |

## Troubleshooting

If you encounter "library not found" errors:
- Ensure libmcpspy.so is in the same directory as the binary
- Or set LD_LIBRARY_PATH to include the userland directory

For macOS users:
- You may need to allow the library in System Preferences > Security & Privacy
- Use DYLD_INSERT_LIBRARIES instead of LD_PRELOAD on macOS