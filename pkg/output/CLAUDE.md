# Output Package

## Overview

The output package provides display handlers for MCP messages. All handlers subscribe to `event.EventTypeMCPMessage` from the event bus and format messages for display.

## Output Modes

### Console (`console.go`)

**Streaming text output with colors**

Enable: Default mode (when `--tui` is not specified)

Capabilities:

- Real-time streaming of messages to stdout
- Optional raw JSON buffers with `--buffers` or `-b` flag (static mode only)
- Statistics table on exit

### TUI (`tui.go`)

**Interactive terminal UI built with Bubbletea**

Enable: `--tui` flag

Capabilities:

- Interactive table view with scrolling
- Filter by transport (t), type (y), actor (a)
- Detail view (Enter) with JSON inspection
- Density modes (d): comfort/compact/ultra
- Pause (p), auto-scroll (f), banner collapse (b)
- Circular buffer (1000 messages max)

Keys: ↑↓/jk=navigate, Enter=details, p=pause, t=transport, y=type, a=actor, f=follow, d=density, b=banner, q=quit

### File Output (`jsonl.go`)

**Background JSON-lines logging**

Enable: `--output /path/to/file.jsonl` or `-o /path/to/file.jsonl`

Capabilities:

- Writes each MCP event as a JSON line
- Runs independently alongside console/TUI
- Non-blocking writes
- Useful for post-processing and analysis

## Architecture

```
Event Bus → EventTypeMCPMessage → Output Handlers
                                      ↓
                              ┌───────┼───────┐
                              ↓       ↓       ↓
                          Console   TUI    File
```

Each handler subscribes independently and processes events asynchronously.
