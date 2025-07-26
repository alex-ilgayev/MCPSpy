# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO/eBPF
RUN apk add --no-cache \
    clang \
    llvm \
    make \
    libbpf-dev \
    build-base

WORKDIR /build

# Copy source code
COPY . .

# Install Go tools (bpf2go)
RUN go install github.com/cilium/ebpf/cmd/bpf2go@latest

# New: Conditional CGO flags based on TARGETARCH
# This is the fix for the '-m64' error on ARM64
RUN if [ "${TARGETARCH}" = "arm64" ]; then \
        echo "Setting CGO_CFLAGS for ARM64 (removing -m64 if present)"; \
        # Explicitly set CGO_CFLAGS to empty or safe flags for ARM64
        # -m64 is specifically for x86_64, not arm64. Overriding to nothing for ARM64.
        export CGO_CFLAGS=""; \
        export CGO_LDFLAGS=""; \
    fi; \
    # Build the binary for the target platform
    echo "Building for TARGETOS=$TARGETOS TARGETARCH=$TARGETARCH" && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=1 make generate && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=1 go build \
        -ldflags "-X github.com/alex-ilgayev/mcpspy/pkg/version.Version=$(git describe --tags --always --dirty 2>/dev/null || echo 'dev') \
                  -X github.com/alex-ilgayev/mcpspy/pkg/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') \
                  -X github.com/alex-ilgayev/mcpspy/pkg/version.Date=$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
        -trimpath \
        -o mcpspy \
        ./cmd/mcpspy

# Runtime stage
FROM alpine:latest

# Copy the binary from builder stage
COPY --from=builder /build/mcpspy /app/mcpspy

# Add labels for better image metadata
LABEL org.opencontainers.image.title="MCPSpy"
LABEL org.opencontainers.image.description="Real-time monitoring for Model Context Protocol communication using eBPF"
LABEL org.opencontainers.image.source="https://github.com/alex-ilgayev/mcpspy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /app

ENTRYPOINT ["/app/mcpspy"]
