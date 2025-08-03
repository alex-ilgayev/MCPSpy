#!/usr/bin/env bash
set -euo pipefail

# Check if mcpspy path is provided
if [ $# -ne 1 ]; then
    echo "Usage: $0 <path-to-mcpspy>"
    exit 1
fi

MCPSPY_PATH="$1"

# Check if the binary exists
if [ ! -f "$MCPSPY_PATH" ]; then
    echo "Error: MCPSpy binary not found at $MCPSPY_PATH"
    exit 1
fi

echo "Starting MCPSpy smoke test..."

# Start MCPSpy in background and capture output
"$MCPSPY_PATH" > mcpspy.out 2> mcpspy.err &
MCPSPY_PID=$!

# Wait a moment to check if process is still running
sleep 2

# Check if process started successfully
if ! kill -0 $MCPSPY_PID 2>/dev/null; then
    echo "Error: MCPSpy failed to start"
    echo "=== stdout ==="
    cat mcpspy.out
    echo "=== stderr ==="
    cat mcpspy.err
    rm -f mcpspy.out mcpspy.err
    exit 1
fi

# Wait additional time to ensure proper initialization
sleep 5

# Send SIGTERM for graceful shutdown
kill -TERM $MCPSPY_PID

# Wait up to 5 seconds for the process to terminate
for i in {1..5}; do
    if ! kill -0 $MCPSPY_PID 2>/dev/null; then
        echo "MCPSpy terminated successfully"
        # Display any output if present
        if [ -s mcpspy.out ]; then
            echo "=== stdout ==="
            cat mcpspy.out
        fi
        if [ -s mcpspy.err ]; then
            echo "=== stderr ==="
            cat mcpspy.err
            # Return failure if there's stderr output
            rm -f mcpspy.out mcpspy.err
            exit 1
        fi
        rm -f mcpspy.out mcpspy.err
        exit 0
    fi
    sleep 1
done

# If process still exists, force kill it
if kill -0 $MCPSPY_PID 2>/dev/null; then
    echo "MCPSpy didn't terminate gracefully, force killing..."
    kill -9 $MCPSPY_PID
    echo "=== stdout ==="
    cat mcpspy.out
    echo "=== stderr ==="
    cat mcpspy.err
    rm -f mcpspy.out mcpspy.err
    exit 1
fi
