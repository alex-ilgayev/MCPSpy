.PHONY: all build generate clean image test test-e2e-setup test-clean

BINARY_NAME := mcpspy
GO ?= go
CLANG ?= clang
CLANG_FORMAT ?= clang-format
DOCKER ?= docker
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X github.com/alex-ilgayev/mcpspy/pkg/version.Version=$(VERSION) -X github.com/alex-ilgayev/mcpspy/pkg/version.Commit=$(COMMIT) -X github.com/alex-ilgayev/mcpspy/pkg/version.Date=$(BUILD_DATE)
BPF_SRCS := $(shell find ./bpf -type f \( -name '*.[ch]' ! -name 'vmlinux.h' \))

DOCKER_IMAGE ?= ghcr.io/alex-ilgayev/mcpspy
IMAGE_TAG ?= latest

# Default target
all: generate build

# Generate eBPF Go bindings
generate:
	@echo "Generating eBPF Go bindings..."
	cd pkg/ebpf && go generate

# Build the binary
build: generate
	@echo "Building $(BINARY_NAME)..."
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/mcpspy

# Build Docker image
image: clean
	@echo "Building Docker image..."
	$(DOCKER) image build --no-cache -t $(DOCKER_IMAGE):$(IMAGE_TAG) -f deploy/docker/Dockerfile .

# Clean test environment
test-e2e-clean:
	@echo "Cleaning test environment..."
	rm -rf tests/venv

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -f pkg/ebpf/mcpspy_bpfe*.go
	rm -f pkg/ebpf/mcpspy_bpfe*.o

# Install dependencies
deps:
	@echo "Installing dependencies..."
	$(GO) mod download
	$(GO) mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	$(CLANG_FORMAT) --verbose -i --Werror -style="{IndentWidth: 4}" $(BPF_SRCS)

# Run linters
lint:
	@echo "Running linters..."
	golangci-lint run --disable=errcheck

# Run unit tests
test:
	@echo "Running unit tests..."
	$(GO) test -v ./...

# Setup e2e test environment
test-e2e-setup:
	@echo "Setting up test environment..."
	python3 -m venv tests/venv || true
	tests/venv/bin/pip install -r tests/requirements.txt

# Run MCP client (without MCPSpy) with simulated traffic
test-e2e-mcp: test-e2e-setup
	@echo "Running MCP client..."
	tests/venv/bin/python tests/mcp_client.py --server "tests/venv/bin/python tests/mcp_server.py"

# Run end-to-end tests
test-e2e: build test-e2e-setup
	@echo "Running end-to-end tests..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E tests/venv/bin/python tests/e2e_test.py --mcpspy ./$(BINARY_NAME)

# Help
help:
	@echo "MCPSpy Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all              - Generate and build (default)"
	@echo "  generate         - Generate eBPF Go bindings"
	@echo "  build            - Build the binary"
	@echo "  image            - Build Docker image"
	@echo "  clean            - Clean build artifacts"
	@echo "  deps             - Install Go dependencies"
	@echo "  fmt              - Format code"
	@echo "  lint             - Run linters"
	@echo "  test             - Run unit tests"
	@echo "  test-e2e-mcp     - Run MCP client (without MCPSpy) with simulated traffic"
	@echo "  test-e2e         - Run end-to-end tests"
	@echo "  test-e2e-clean   - Clean Python e2e test environment"
	@echo "  test-e2e-setup   - Setup Python e2e test environment"
	@echo "  help             - Show this help" 