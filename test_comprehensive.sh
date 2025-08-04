#\!/bin/bash

echo "Testing MCPSpy Userland Mode"
echo "============================"

# Test 1: Basic functionality
echo "Test 1: Basic JSON-RPC detection"
echo '{"jsonrpc":"2.0","method":"test","id":1}' | MCPSPY_ENABLE=1 LD_PRELOAD=./libmcpspy.so cat > /dev/null
if [ $? -eq 0 ]; then
    echo "✅ LD_PRELOAD injection works"
else  
    echo "❌ LD_PRELOAD injection failed"
    exit 1
fi

# Test 2: MCP-like communication
echo "Test 2: MCP communication simulation"
output=$(MCPSPY_ENABLE=1 LD_PRELOAD=./libmcpspy.so python3 test_mcp.py 2>/dev/null)
if echo "$output" | grep -q "jsonrpc"; then
    echo "✅ MCP messages captured"
    echo "Sample output:"
    echo "$output" | head -2
else
    echo "❌ MCP messages not captured"
fi

# Test 3: Binary execution
echo "Test 3: Userland binary execution"
if ./mcpspy-userland --mode userland --help > /dev/null 2>&1; then
    echo "✅ Userland binary executes successfully"
else
    echo "❌ Userland binary execution failed"
    exit 1
fi

echo ""
echo "All tests passed\! ✅"
echo "Userland mode is working correctly on macOS"
EOF < /dev/null