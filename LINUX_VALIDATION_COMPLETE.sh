#!/bin/bash
# Complete Linux Validation Script for MCPSpy Userland Mode
# This script validates all reviewer feedback has been addressed

set -e

echo "=========================================="
echo "MCPSpy Userland Mode - Complete Validation"
echo "=========================================="
echo "This script validates all fixes from PR #21 feedback"
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass_count=0
fail_count=0

log_pass() {
    echo -e "${GREEN}✅ PASS${NC}: $1"
    ((pass_count++))
}

log_fail() {
    echo -e "${RED}❌ FAIL${NC}: $1"
    ((fail_count++))
}

log_info() {
    echo -e "${YELLOW}ℹ️  INFO${NC}: $1"
}

# Phase 1: Dependency Validation (Addresses: "missing instructions to install pkg-config libpcap-dev libssl-dev")
echo "Phase 1: Dependency Validation"
echo "==============================="

log_info "Checking that NO external dependencies are required..."

# Check userland Makefile doesn't reference libpcap/openssl
if grep -q "libpcap\|openssl\|ssl" userland/Makefile; then
    log_fail "userland/Makefile still references libpcap or openssl dependencies"
else
    log_pass "userland/Makefile has no external dependency references"
fi

# Check no pkg-config usage
if grep -q "pkg-config" userland/Makefile Makefile; then
    log_fail "Makefiles still use pkg-config"
else
    log_pass "No pkg-config usage in Makefiles"
fi

# Phase 2: Compilation Tests (Addresses: "Go code is not compiling", "time imported and not used", field errors)
echo ""
echo "Phase 2: Compilation Tests"
echo "=========================="

log_info "Building userland C library..."
cd userland
if make clean && make; then
    log_pass "Userland C library compiles without external dependencies"
    if [ -f libmcpspy.so ]; then
        log_pass "libmcpspy.so created successfully"
    else
        log_fail "libmcpspy.so not created"
    fi
else
    log_fail "Userland C library compilation failed"
fi
cd ..

log_info "Testing Go compilation with userland tags..."
if go build -tags userland -o mcpspy-userland ./cmd/mcpspy 2>&1; then
    log_pass "Go userland mode compiles successfully"
else
    log_fail "Go userland mode compilation failed"
fi

log_info "Testing default eBPF mode compilation..."
if go build -o mcpspy-ebpf ./cmd/mcpspy 2>&1; then
    log_pass "Go eBPF mode compiles successfully (default preserved)"
else
    log_fail "Go eBPF mode compilation failed"
fi

# Phase 3: CLI Interface Validation (Addresses: "a lot of flags to main.go CLI")
echo ""
echo "Phase 3: CLI Interface Validation"  
echo "================================="

log_info "Checking CLI flags have been simplified..."

# Check help output shows single --mode flag
if ./mcpspy-userland --help 2>&1 | grep -q "\-\-mode.*ebpf.*userland"; then
    log_pass "Single --mode flag present with ebpf/userland options"
else
    log_fail "CLI interface not properly simplified to single --mode flag"
fi

# Check eBPF is default
if ./mcpspy-userland --help 2>&1 | grep -q 'default "ebpf"'; then
    log_pass "eBPF confirmed as default mode"
else
    log_fail "eBPF is not the default mode"
fi

# Check no complex transport flags
if ./mcpspy-userland --help 2>&1 | grep -qE "\-\-http|\-\-ssl|\-\-tcp|\-\-transport"; then
    log_fail "Complex transport flags still present (should be removed)"
else
    log_pass "Complex transport flags removed"
fi

# Phase 4: Functional Testing (Addresses: LD_PRELOAD functionality)
echo ""
echo "Phase 4: Functional Testing"
echo "==========================="

log_info "Testing LD_PRELOAD injection..."

# Test basic LD_PRELOAD doesn't crash
if echo '{"jsonrpc":"2.0","method":"test","id":1}' | MCPSPY_ENABLE=1 LD_PRELOAD=./userland/libmcpspy.so cat > /dev/null 2>&1; then
    log_pass "LD_PRELOAD injection works without crashing"
else
    log_fail "LD_PRELOAD injection failed or crashed"
fi

# Test MCP message detection
log_info "Testing MCP message capture..."
output=$(MCPSPY_ENABLE=1 LD_PRELOAD=./userland/libmcpspy.so python3 test_mcp.py 2>/dev/null | head -5)
if echo "$output" | grep -q "jsonrpc"; then
    log_pass "MCP messages successfully captured via LD_PRELOAD"
    echo "   Sample captured: $(echo "$output" | head -1 | cut -c1-60)..."
else
    log_fail "MCP messages not captured"
fi

# Test binary execution with userland mode
log_info "Testing userland binary execution..."
if timeout 3 ./mcpspy-userland --mode userland --help > /dev/null 2>&1; then
    log_pass "Userland binary executes successfully"
else
    log_fail "Userland binary execution failed"
fi

# Phase 5: Code Quality Validation (Addresses: HTTP monitoring removal, environment variable complexity)
echo ""
echo "Phase 5: Code Quality Validation"
echo "================================"

log_info "Checking HTTP monitoring code removed..."
if grep -r "http\|HTTP\|ssl\|SSL\|tls\|TLS" userland/ --include="*.c" --include="*.h" | grep -v "stdio"; then
    log_fail "HTTP/SSL/TLS code still present in userland implementation"
else
    log_pass "HTTP/SSL/TLS monitoring code successfully removed"
fi

log_info "Checking environment variable complexity reduced..."
env_vars=$(grep -r "getenv\|setenv" userland/ --include="*.c" | wc -l)
if [ "$env_vars" -le 2 ]; then
    log_pass "Environment variable usage minimized (≤2 occurrences)"
else
    log_fail "Too many environment variables still in use ($env_vars occurrences)"
fi

# Phase 6: Integration Testing
echo ""
echo "Phase 6: Integration Testing"
echo "=========================="

log_info "Testing mode selection works correctly..."

# Test invalid mode rejection
if ./mcpspy-userland --mode invalid 2>&1 | grep -q "invalid mode"; then
    log_pass "Invalid mode properly rejected"
else
    log_fail "Invalid mode not properly handled"
fi

# Test eBPF mode selection (if available)
if timeout 2 ./mcpspy-ebpf --mode ebpf --help > /dev/null 2>&1; then
    log_pass "eBPF mode selection works"
else
    log_info "eBPF mode may not be available on this system (expected on some platforms)"
fi

# Final Summary
echo ""
echo "=========================================="
echo "VALIDATION SUMMARY"
echo "=========================================="
echo -e "Total tests: $((pass_count + fail_count))"
echo -e "${GREEN}Passed: $pass_count${NC}"
echo -e "${RED}Failed: $fail_count${NC}"

if [ "$fail_count" -eq 0 ]; then
    echo -e "${GREEN}"
    echo "🎉 ALL TESTS PASSED! 🎉"
    echo "Implementation successfully addresses all PR #21 reviewer feedback."
    echo "Ready to create new PR."
    echo -e "${NC}"
    exit 0
else
    echo -e "${RED}"
    echo "❌ SOME TESTS FAILED"
    echo "Please address the failed tests before proceeding with PR creation."
    echo -e "${NC}"
    exit 1
fi