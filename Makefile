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

# =============================================================================
# Test Functions
# =============================================================================
# These functions reduce duplication across test targets

# Run scenario only (no MCPSpy)
# $(1) = scenario name
define run-scenario
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario $(1) \
		--skip-mcpspy
endef

# Run e2e test with MCPSpy
# $(1) = scenario name
define run-e2e
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario $(1)
endef

# Update expected output for e2e test
# $(1) = scenario name
define run-update
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--scenario $(1) \
		--update-expected
endef

# =============================================================================
##@ Build Targets
# =============================================================================

.PHONY: all
all: generate build ## Build everything (default target)

.PHONY: generate
generate: ## Generate eBPF Go bindings
	@echo "Generating eBPF Go bindings..."
	cd pkg/ebpf && MCPSPY_TRACE_LOG=$(MCPSPY_TRACE_LOG) go generate

.PHONY: build
build: generate ## Build the binary for current platform
	@echo "Building $(BINARY_NAME) for $(PLATFORM)..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(BUILD_FLAGS) -o $(BINARY_OUTPUT) ./cmd/mcpspy
	@echo "Binary built: $(BINARY_OUTPUT)"

.PHONY: build-platforms
build-platforms: generate ## Build binaries for all supported platforms
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
checksums: ## Generate sha256 checksums for all built binaries
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

.PHONY: image
image: build checksums ## Build Docker image for current platform
	@echo "Building Docker image for $(PLATFORM)..."
	@sha256sum -c $(BINARY_OUTPUT).sha256sum || exit 1
	@cp $(BINARY_OUTPUT) ./mcpspy
	@if docker buildx version >/dev/null 2>&1; then \
        echo "Using Docker Buildx for current platform..."; \
        $(DOCKER) buildx build --load --platform=$(GOOS)/$(GOARCH) -t $(DOCKER_IMAGE):$(IMAGE_TAG) -f deploy/docker/Dockerfile .; \
    else \
        echo "Using legacy Docker build (buildx not available)..."; \
        $(DOCKER) image build --no-cache -t $(DOCKER_IMAGE):$(IMAGE_TAG) -f deploy/docker/Dockerfile .; \
    fi;
	@echo "Docker image built: $(DOCKER_IMAGE):$(IMAGE_TAG)"

# =============================================================================
##@ Development Tools
# =============================================================================

.PHONY: deps
deps: ## Install Go dependencies
	@echo "Installing dependencies..."
	$(GO) mod download
	$(GO) mod tidy

.PHONY: go-tools
go-tools: ## Install Go development tools (golangci-lint, bpf2go)
	@echo "Installing Go development tools..."
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GO) install github.com/cilium/ebpf/cmd/bpf2go@latest
	@echo "Please ensure $(GO_BIN_DIR) is in your PATH for local development."

.PHONY: fmt
fmt: ## Format Go and BPF code
	@echo "Formatting code..."
	$(GO) fmt ./...
	$(CLANG_FORMAT) --verbose -i --Werror -style="{IndentWidth: 4}" $(BPF_SRCS)

.PHONY: lint
lint: generate ## Run linters
	@echo "Running linters..."
	$(GO_BIN_DIR)/$(GOLANGCI_LINT) run --disable=errcheck

# =============================================================================
##@ Unit & Integration Tests
# =============================================================================

.PHONY: test
test: test-unit test-e2e ## Run all tests (unit and e2e)

.PHONY: test-unit
test-unit: ## Run unit tests
	@echo "Running unit tests..."
	$(GO) test $(TEST_FLAGS) ./...

.PHONY: test-integration
test-integration: ## Run integration tests (requires HF_TOKEN, skips if missing)
	@echo "Running integration tests..."
	@if [ -z "$$HF_TOKEN" ]; then \
		echo "⏭️  Skipping integration tests: HF_TOKEN environment variable is not set"; \
		echo "   Set HF_TOKEN to run these tests: HF_TOKEN=hf_xxx make test-integration"; \
	else \
		$(GO) test -v -tags=integration -timeout=300s ./pkg/security/...; \
	fi

# =============================================================================
##@ Test Setup & Utilities
# =============================================================================

.PHONY: test-e2e-setup
test-e2e-setup: ## Setup Python e2e test environment
	@echo "Setting up test environment..."
	@$(PYTHON) -m venv tests/venv || true
	tests/venv/bin/pip install -r tests/requirements.txt

.PHONY: test-e2e-clean
test-e2e-clean: ## Clean Python e2e test environment
	@echo "Cleaning test environment..."
	rm -rf tests/venv

.PHONY: test-smoke
test-smoke: ## Run smoke test (basic startup/shutdown)
	@echo "Running smoke test..."
	@chmod +x tests/smoke_test.sh
	@chmod +x $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)
	tests/smoke_test.sh $(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)

# =============================================================================
##@ Scenario Tests (no MCPSpy)
# =============================================================================

.PHONY: test-scenario-stdio
test-scenario-stdio: test-e2e-setup ## Run stdio scenario (no MCPSpy)
	@echo "Running stdio scenario..."
	$(call run-scenario,stdio-fastmcp)

.PHONY: test-scenario-https
test-scenario-https: test-e2e-setup ## Run HTTPS scenario (no MCPSpy)
	@echo "Running HTTPS scenario..."
	$(call run-scenario,http-fastmcp)

.PHONY: test-scenario-claudecode
test-scenario-claudecode: test-e2e-setup ## Run Claude Code scenario (no MCPSpy)
	@echo "Running Claude Code scenario..."
	$(call run-scenario,claude-code-init)

.PHONY: test-scenario-llm-anthropic
test-scenario-llm-anthropic: test-e2e-setup ## Run Anthropic LLM scenario (no MCPSpy, requires CLAUDE_CODE_OAUTH_TOKEN)
	@echo "Running Anthropic LLM scenario..."
	$(call run-scenario,llm-anthropic)

.PHONY: test-scenario-llm-gemini
test-scenario-llm-gemini: test-e2e-setup ## Run Gemini LLM scenario (no MCPSpy, requires GEMINI_API_KEY)
	@echo "Running Gemini LLM scenario..."
	$(call run-scenario,llm-gemini)

.PHONY: test-scenario-security
test-scenario-security: test-e2e-setup ## Run security scenario (no MCPSpy)
	@echo "Running security scenario..."
	$(call run-scenario,security-injection)

.PHONY: test-scenario-gemini-cli
test-scenario-gemini-cli: test-e2e-setup ## Run Gemini CLI scenario (no MCPSpy, requires GEMINI_API_KEY)
	@echo "Running Gemini CLI scenario..."
	$(call run-scenario,gemini-cli)

# =============================================================================
##@ E2E Tests (with MCPSpy)
# =============================================================================

.PHONY: test-e2e-stdio
test-e2e-stdio: build test-e2e-setup ## Run e2e test for stdio transport
	@echo "Running e2e test for stdio transport..."
	$(call run-e2e,stdio-fastmcp)

.PHONY: test-e2e-https
test-e2e-https: build test-e2e-setup ## Run e2e test for HTTPS transport
	@echo "Running e2e test for HTTPS transport..."
	$(call run-e2e,http-fastmcp)

.PHONY: test-e2e-claudecode
test-e2e-claudecode: build test-e2e-setup ## Run e2e test for Claude Code
	@echo "Running e2e test for Claude Code..."
	$(call run-e2e,claude-code-init)

.PHONY: test-e2e-llm-anthropic
test-e2e-llm-anthropic: build test-e2e-setup ## Run e2e test for Anthropic LLM (requires CLAUDE_CODE_OAUTH_TOKEN)
	@echo "Running Anthropic LLM e2e test..."
	$(call run-e2e,llm-anthropic)

.PHONY: test-e2e-llm-gemini
test-e2e-llm-gemini: build test-e2e-setup ## Run e2e test for Gemini LLM (requires GEMINI_API_KEY)
	@echo "Running Gemini LLM e2e test..."
	$(call run-e2e,llm-gemini)

.PHONY: test-e2e-security
test-e2e-security: build test-e2e-setup ## Run e2e test for security (requires HF_TOKEN, skips if missing)
	@echo "Running security e2e test..."
	$(call run-e2e,security-injection)

.PHONY: test-e2e-gemini-cli
test-e2e-gemini-cli: build test-e2e-setup ## Run e2e test for Gemini CLI (requires GEMINI_API_KEY)
	@echo "Running Gemini CLI e2e test..."
	$(call run-e2e,gemini-cli)

.PHONY: test-e2e
test-e2e: build test-e2e-setup ## Run all e2e test scenarios
	@echo "Running all e2e test scenarios..."
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml

# =============================================================================
##@ Update Expected Output
# =============================================================================

.PHONY: test-update-stdio
test-update-stdio: build test-e2e-setup ## Update expected output for stdio
	@echo "Updating expected output for stdio..."
	$(call run-update,stdio-fastmcp)

.PHONY: test-update-https
test-update-https: build test-e2e-setup ## Update expected output for HTTPS
	@echo "Updating expected output for HTTPS..."
	$(call run-update,http-fastmcp)

.PHONY: test-update-llm-anthropic
test-update-llm-anthropic: build test-e2e-setup ## Update expected output for Anthropic LLM
	@echo "Updating expected output for Anthropic LLM..."
	$(call run-update,llm-anthropic)

.PHONY: test-update-llm-gemini
test-update-llm-gemini: build test-e2e-setup ## Update expected output for Gemini LLM
	@echo "Updating expected output for Gemini LLM..."
	$(call run-update,llm-gemini)

.PHONY: test-update-all
test-update-all: build test-e2e-setup ## Update expected output for all scenarios
	@echo "Updating expected output for all scenarios..."
	tests/venv/bin/python tests/e2e_test.py \
		--config tests/e2e_config.yaml \
		--update-expected

# =============================================================================
##@ Cleanup
# =============================================================================

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(BUILD_DIR)
	rm -f pkg/ebpf/mcpspy_bpfe*.go
	rm -f pkg/ebpf/mcpspy_bpfe*.o

# =============================================================================
##@ Help
# =============================================================================

.PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1m%s\033[0m\n", "Usage: make <target>"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		' $(MAKEFILE_LIST)
