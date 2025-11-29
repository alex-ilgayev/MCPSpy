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
LDFLAGS := -X github.com/alex-ilgayev/mcpspy/pkg/version.Version=$(VERSION) \
		   -X github.com/alex-ilgayev/mcpspy/pkg/version.Commit=$(COMMIT) \
		   -X github.com/alex-ilgayev/mcpspy/pkg/version.Date=$(BUILD_DATE)

# Source files
BPF_SRCS := $(shell find ./bpf -type f \( -name '*.[ch]' ! -name 'vmlinux.h' \))

# Build configuration
CGO_ENABLED ?= 0
BUILD_FLAGS := -ldflags "$(LDFLAGS)"
TEST_FLAGS := -v -timeout=30s

# eBPF build flags
# Set MCPSPY_TRACE_LOG=1 to enable compile-time TRACE logging
# Example: make build MCPSPY_TRACE_LOG=1
MCPSPY_TRACE_LOG ?= 0

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
	cd pkg/ebpf && MCPSPY_TRACE_LOG=$(MCPSPY_TRACE_LOG) go generate

# Build the binary
.PHONY: build
build: generate	## Build the binary for current platform
	@echo "Building $(BINARY_NAME) for $(PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(BUILD_FLAGS) -o $(BINARY_OUTPUT) ./cmd/mcpspy
	@echo "Binary built: $(BINARY_OUTPUT)"

.PHONY: build-platforms
build-platforms: generate ## Build the binaries for all supported platforms
	@echo "Building $(BINARY_NAME) for all platforms..."
	@mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d'-' -f1); \
		arch=$$(echo $$platform | cut -d'-' -f2); \
		echo "Building for $$platform ($$os/$$arch)..."; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-$$platform ./cmd/mcpspy; \
		echo "Built: $(BUILD_DIR)/$(BINARY_NAME)-$$platform"; \
	done
	@echo "All binaries built successfully!"

.PHONY: checksums
checksums: ## Generate sha256 checksums for all built binaries (relative to build/)
	@echo "Generating checksums..."
	@cd $(BUILD_DIR) && \
	for platform in $(PLATFORMS); do \
		bin="$(BINARY_NAME)-$$platform"; \
		if [ -f "$$bin" ]; then \
			echo "Creating checksum for $$bin"; \
			sha256sum "$$bin" > "$$bin.sha256sum"; \
			echo "Created: $$bin.sha256sum"; \
		fi; \
	done

.PHONY: release-assets
release-assets: build-platforms checksums ## Build binaries and generate checksums

# Build Docker image for current platform (optional, CI handles multi-platform)
.PHONY: image
image: build ## Build Docker image for current platform (local development)
	@echo "Building Docker image for $(PLATFORM)..."
	# Check checksum
	@sha256sum -c $(BINARY_OUTPUT).sha256sum || exit 1
	# Copy the binary from the build directory to the current directory
	@cp $(BINARY_OUTPUT) ./mcpspy
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

# Run all tests (unit and e2e for both transports)
.PHONY: test
test: test-unit test-e2e ## Run all tests (unit and e2e for both transports)

# Run unit tests only
.PHONY: test-unit
test-unit: ## Run unit tests
	@echo "Running unit tests..."
	$(GO) test $(TEST_FLAGS) ./...

# Setup e2e test environment
.PHONY: test-e2e-setup
test-e2e-setup: ## Setup Python e2e test environment
	@echo "Setting up test environment..."
	@$(PYTHON) -m venv tests/venv || true
	tests/venv/bin/pip install -r tests/requirements.txt

# Run e2e scenarios without MCPSpy (traffic generation only) - stdio transport
.PHONY: test-e2e-mcp-stdio
test-e2e-mcp-stdio: test-e2e-setup ## Run e2e test without MCPSpy for stdio transport
	@echo "Running e2e test without MCPSpy for stdio transport..."
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario stdio-fastmcp \
		--skip-mcpspy

# Run e2e scenarios without MCPSpy (traffic generation only) - HTTPS transport
.PHONY: test-e2e-mcp-https
test-e2e-mcp-https: test-e2e-setup ## Run e2e test without MCPSpy for HTTPS transport
	@echo "Running e2e test without MCPSpy for HTTPS transport..."
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario http-fastmcp \
		--skip-mcpspy

# Run end-to-end test for stdio transport
.PHONY: test-e2e-stdio
test-e2e-stdio: build test-e2e-setup ## Run end-to-end test for stdio transport
	@echo "Running end-to-end test for stdio transport..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario stdio-fastmcp

# Run end-to-end test for HTTP transport
.PHONY: test-e2e-https
test-e2e-https: build test-e2e-setup ## Run end-to-end test for HTTP transport
	@echo "Running end-to-end test for HTTP transport..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario http-fastmcp

# Run e2e scenarios without MCPSpy (traffic generation only) - Claude Code
.PHONY: test-e2e-mcp-claude
test-e2e-mcp-claude: test-e2e-setup ## Run e2e test without MCPSpy for Claude Code
	@echo "Running e2e test without MCPSpy for Claude Code..."
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario claude-code-init \
		--skip-mcpspy

# Run end-to-end test for Claude Code
.PHONY: test-e2e-claude
test-e2e-claude: build test-e2e-setup ## Run end-to-end test for Claude Code
	@echo "Running end-to-end test for Claude Code..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario claude-code-init

# Run end-to-end tests for all transports
.PHONY: test-e2e
test-e2e: build test-e2e-setup ## Run end-to-end tests for all transports
	@echo "Running all end-to-end test scenarios..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml

# Update expected output files for stdio transport
.PHONY: test-e2e-update-stdio
test-e2e-update-stdio: build test-e2e-setup ## Update expected output files for stdio transport
	@echo "Updating expected output for stdio transport..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario stdio-fastmcp \
		--update-expected

# Update expected output files for HTTP transport
.PHONY: test-e2e-update-https
test-e2e-update-https: build test-e2e-setup ## Update expected output files for HTTP transport
	@echo "Updating expected output for HTTP transport..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario http-fastmcp \
		--update-expected

# Update expected output files for all transports
.PHONY: test-e2e-update
test-e2e-update: build test-e2e-setup ## Update expected output files for all transports
	@echo "Updating expected output for all scenarios..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	sudo -E env PATH="$$PATH" tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--update-expected

.PHONY: test-smoke
test-smoke: ## Run smoke test (basic startup/shutdown test)
	@echo "Running smoke test..."
	@echo "Note: MCPSpy requires root privileges for eBPF operations"
	@chmod +x tests/smoke_test.sh
	@chmod +x $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)
	sudo -E env PATH="$$PATH" tests/smoke_test.sh $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)

# Help
PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1m%s\033[0m\n", "Usage: make <target>"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		' $(MAKEFILE_LIST)
