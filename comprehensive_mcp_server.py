#!/usr/bin/env python3
"""
Simple test script to generate MCP-like JSON-RPC messages on stdio
for testing the userland monitoring mode
"""

import json
import sys
import time

# Test MCP messages
messages = [
    {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "test-client", "version": "1.0.0"}
        }
    },
    {
        "jsonrpc": "2.0",
        "id": 1,
        "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "serverInfo": {"name": "test-server", "version": "1.0.0"}
        }
    },
    {
        "jsonrpc": "2.0",
        "id": 2,
        "method": "resources/list",
        "params": {}
    },
    {
        "jsonrpc": "2.0",
        "id": 2,
        "result": {
            "resources": [
                {
                    "uri": "file://test.txt",
                    "name": "Test Resource",
                    "mimeType": "text/plain"
                }
            ]
        }
    }
]

if __name__ == "__main__":
    print("Starting MCP test script...", file=sys.stderr)
    
    for i, msg in enumerate(messages):
        json_str = json.dumps(msg) + "\n"
        print(f"Sending message {i+1}: {msg['method'] if 'method' in msg else 'response'}", file=sys.stderr)
        sys.stdout.write(json_str)
        sys.stdout.flush()
        time.sleep(0.5)
    
    print("Test complete.", file=sys.stderr)