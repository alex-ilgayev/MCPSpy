# Default target
.DEFAULT_GOAL := help

# Project configuration
BINARY_NAME := mcpspy
DOCKER_IMAGE ?= ghcr.io/alex-ilgayev/mcpspy
IMAGE_TAG ?= latest

# Tools
GO ?= go
CLANG ?= clang
CLANG_FORMAT ?= clang-format
DOCKER ?= docker
PYTHON ?= python3
GOLANGCI_LINT ?= golangci-lint

# Platform detection and cross-compilation support
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
PLATFORM := $(GOOS)-$(GOARCH)

# Supported platforms for cross-compilation
PLATFORMS := linux-amd64 linux-arm64

# Version information
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
# Note: LDFLAGS are now primarily defined directly in the CI workflow Go build commands,
# and also used within the Dockerfile's build step.
# For local `make build`, they are implicitly used by ./cmd/mcpspy build.

# Source files
BPF_SRCS := $(shell find ./bpf -type f \( -name '*.[ch]' ! -name 'vmlinux.h' \))

# Directories
BUILD_DIR := build
GO_BIN_DIR := $(shell go env GOPATH)/bin

# Binary naming with platform suffix
BINARY_OUTPUT := $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)

##@ Build Targets

# Default target
.PHONY: all
all: generate build ## Building everything (default target)

# Generate eBPF Go bindings
.PHONY: generate
generate: ## Generate eBPF Go bindings
	@echo "Generating eBPF Go bindings..."
	cd pkg/ebpf && go generate

# Build the binary
.PHONY: build
build: generate	## Build the binary for current platform
	@echo "Building $(BINARY_NAME) for $(PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	# CGO_ENABLED MUST BE 1 for eBPF programs!
	@CGO_ENABLED=1 $(GO) build \
		-ldflags "-X github.com/alex-ilgayev/mcpspy/pkg/version.Version=$(VERSION) \
				  -X github.com/alex-ilgayev/mcpspy/pkg/version.Commit=$(COMMIT) \
				  -X github.com/alex-ilgayev/mcpspy/pkg/version.Date=$(BUILD_DATE)" \
		-trimpath \
		-o $(BINARY_OUTPUT) \
		./cmd/mcpspy
	@echo "Binary built: $(BINARY_OUTPUT)"

# Build Docker image for current platform (optional, CI handles multi-platform)
.PHONY: image
image: ## Build Docker image for current platform (local development)
	@echo "Building Docker image for $(PLATFORM)..."
	@if docker buildx version >/dev/null 2>&1; then \
        echo "Using Docker Buildx for current platform..."; \
        $(DOCKER) buildx build --load --platform=$(GOOS)/$(GOARCH) -t $(DOCKER_IMAGE):$(IMAGE_TAG) -f deploy/docker/Dockerfile .; \
    else \
        echo "Using legacy Docker build (buildx not available)..."; \
        $(DOCKER) image build --no-cache -t $(DOCKER_IMAGE):$(IMAGE_TAG) -f deploy/docker/Dockerfile .; \
    fi;
	@echo "Docker image built: $(DOCKER_IMAGE):$(IMAGE_TAG)"

##@ Test Targets

# Clean test environment
.PHONY: test-e2e-clean
test-e2e-clean: ## Clean Python e2e test environment
	@echo "Cleaning test environment..."
	rm -rf tests/venv

# Clean build artifacts
.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f pkg/ebpf/mcpspy_bpfe*.go
	rm -f pkg/ebpf/mcpspy_bpfe*.o

# Install dependencies
.PHONY: deps
deps: ## Install dependencies
	@echo "Installing dependencies..."
	$(GO) mod download
	$(GO) mod tidy

PHONY: go-tools
go-tools: ## Install Go development tools (golangci-lint, bpf2go)
	@echo "Installing Go development tools..."
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GO) install github.com/cilium/ebpf/cmd/bpf2go@latest
	@echo "Please ensure $(GO_BIN_DIR) is in your PATH for local development."

# Format code
.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	$(GO) fmt ./...
	$(CLANG_FORMAT) --verbose -i --Werror -style="{IndentWidth: 4}" $(BPF_SRCS)

# Run linters
.PHONY: lint
lint: generate ## Run linters
	@echo "Running linters..."
	$(GO_BIN_DIR)/$(GOLANGCI_LINT) run --disable=errcheck

# Run unit tests
.PHONY: test
test: ## Run unit tests
	@echo "Running unit tests..."
	$(GO) test -v ./...

# Setup e2e test environment
.PHONY: test-e2e-setup
test-e2e-setup: ## Setup Python e2e test environment
	@echo "Setting up test environment..."
	@$(PYTHON) -m venv tests/venv || true
	tests/venv/bin/pip install -r tests/requirements.txt

# Run MCP client (without MCPSpy) with simulated traffic
.PHONY: test-e2e-mcp
test-e2e-mcp: test-e2e-setup ## Run MCP client (without MCPSpy) with simulated traffic
	@echo "Running MCP client..."
	tests/venv/bin/python tests/mcp_client.py --server "tests/venv/bin/python tests/mcp_server.py"

# Run end-to-end tests
.PHONY: test-e2e
test-e2e: build test-e2e-setup ## Run end-to-end tests
	@echo "Running end-to-end tests..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	# Ensure this points to the locally built binary
	sudo -E tests/venv/bin/python tests/e2e_test.py --mcpspy $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)

# Help
PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1m%s\033[0m\n", "Usage: make <target>"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		' $(MAKEFILE_LIST)
