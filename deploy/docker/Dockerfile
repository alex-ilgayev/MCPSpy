# We no longer need a 'builder' stage for compilation.
# We will receive the pre-built binary as part of the build context.

# IMPORTANT: The base image needs to match the target architecture that Buildx is building for.
# Buildx handles this automatically with `FROM alpine:X.X`.
FROM alpine:3.22

# Copy the pre-built binary that will be passed into the build context.
# We'll expect the binary to be at the root of the build context (which we'll achieve with the CI action).
# The name of the binary will be `mcpspy` inside the image.
COPY mcpspy /app/mcpspy
# Make the binary executable
RUN chmod +x /app/mcpspy

# Add labels for better image metadata
LABEL org.opencontainers.image.title="MCPSpy"
LABEL org.opencontainers.image.description="Real-time monitoring for Model Context Protocol communication using eBPF"
LABEL org.opencontainers.image.source="https://github.com/alex-ilgayev/mcpspy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /app

ENTRYPOINT ["/app/mcpspy"]
