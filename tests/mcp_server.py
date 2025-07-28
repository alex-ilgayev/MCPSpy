#!/usr/bin/env python3
"""
Comprehensive MCP Server Example using FastMCP SDK
==================================================

This server demonstrates various MCP capabilities including:
- Tools for different use cases
- Resources (static configuration, dynamic data)
- Prompts
- Progress reporting and notifications
- Error handling and logging
- Configurable transport layers (stdio, HTTP, SSE)

Run the server:
    python mcp_server.py (default transport is stdio)
    python mcp_server.py --transport stdio
    python mcp_server.py --transport streamable-http (default endpoint is http://localhost:8000/mcp)
    python mcp_server.py --transport sse (default endpoint is http://localhost:8000/sse)

Run the server with a self-signed certificate (transport is streamable-http):
    uvicorn mcp_server:app --host 0.0.0.0 --port 12345 --ssl-keyfile=server.key --ssl-certfile=server.crt

Or use with MCP development tools:
    mcp dev mcp_server.py
"""

import argparse
import asyncio
import json
import sys
from typing import List

from mcp.server.fastmcp import Context, FastMCP


# Initialize the MCP server
mcp = FastMCP(
    name="Comprehensive MCP Demo Server",
    version="1.0.0",
    description="A comprehensive MCP server demonstrating various capabilities for testing MCPSpy",
)
app = mcp.streamable_http_app()


# =============================================================================
# TRANSPORT CONFIGURATION
# =============================================================================


class TransportConfig:
    """Configuration for different transport layers."""

    def __init__(self):
        self.transport_type = "stdio"

    @classmethod
    def from_args(cls, args):
        """Create TransportConfig from command line arguments."""
        config = cls()
        config.transport_type = args.transport
        return config


def parse_arguments():
    """Parse command line arguments for transport configuration."""
    parser = argparse.ArgumentParser(
        description="Comprehensive MCP Server with configurable transport layers",
        formatter_class=argparse.RawTextHelpFormatter,
    )

    parser.add_argument(
        "--transport",
        "-t",
        choices=["stdio", "streamable-http", "sse"],
        default="stdio",
        help="Transport layer to use (default: stdio)",
    )

    return parser.parse_args()


# =============================================================================
# TOOLS - Functions that LLMs can call to perform actions
# =============================================================================


@mcp.tool()
def get_weather(city: str, units: str = "metric") -> str:
    """
    Get current weather information for a city.

    Args:
        city: Name of the city
        units: Temperature units (metric, imperial, or kelvin)

    Returns:
        Weather information or error message
    """
    try:
        if units == "metric":
            temp = 20
            unit = "°C"
        elif units == "imperial":
            temp = 68
            unit = "°F"
        else:  # kelvin
            temp = 293
            unit = "K"

        return f"Weather in {city}: {temp}{unit}"
    except Exception as e:
        return f"Error getting weather: {str(e)}"


@mcp.tool()
async def process_data_with_progress(
    data: List[str], operation: str, ctx: Context
) -> str:
    """
    Process a list of data items with progress reporting.

    Args:
        data: List of data items to process
        operation: Type of operation (uppercase, lowercase, reverse)
        ctx: MCP context for progress reporting

    Returns:
        Processed data results
    """
    await ctx.info(f"Starting {operation} operation on {len(data)} items")

    results = []
    total = len(data)

    for i, item in enumerate(data):
        # Report progress
        progress = (i + 1) / total
        await ctx.report_progress(
            progress=progress,
            message=f"Processing item {i + 1}/{total}: {item[:20]}...",
        )

        # Process the item
        if operation == "uppercase":
            processed = item.upper()
        elif operation == "lowercase":
            processed = item.lower()
        elif operation == "reverse":
            processed = item[::-1]
        else:
            processed = item

        results.append(processed)

        # Simulate some processing time
        await asyncio.sleep(0.1)

    await ctx.info(f"Completed {operation} operation")
    return f"Processed {total} items: {json.dumps(results, indent=2)}"


# =============================================================================
# RESOURCES - Data that can be read by LLMs
# =============================================================================


@mcp.resource("status://server", mime_type="application/json")
def server_status() -> str:
    """Current server status and health information."""
    status = {
        "status": "healthy",
        "uptime": "2 hours 34 minutes",
        "memory_usage": "45.2 MB",
        "cpu_usage": "12.5%",
        "active_connections": 3,
        "requests_processed": 1247,
    }
    return json.dumps(status, indent=2)


@mcp.resource("file://logs/{log_file}")
def sample_data(log_file: str) -> str:
    """
    Reads a log file.

    Args:
        log_file: Name of the log file to read
    """
    if log_file == "log_a.txt":
        with open("logs/log_a.txt", "r") as f:
            return f.read()
    elif log_file == "log_b.txt":
        with open("logs/log_b.txt", "r") as f:
            return f.read()
    else:
        raise ValueError(f"Unknown log file: {log_file}")


# =============================================================================
# PROMPTS - Templates for LLM interactions
# =============================================================================


@mcp.prompt(title="Code Review")
def code_review(code: str, language: str = "python") -> str:
    """
    Generate a code review prompt.

    Args:
        code: Code to review
        language: Programming language
    """
    return f"""Please review this {language} code and provide feedback on:

1. Code quality and style
2. Potential bugs or issues
3. Performance considerations
4. Security concerns
5. Suggestions for improvement

```{language}
{code}
```

Please provide specific, actionable feedback."""


# =============================================================================
# SERVER STARTUP WITH TRANSPORT CONFIGURATION
# =============================================================================


def start_server_with_transport(config: TransportConfig):
    """Start the MCP server with the specified transport configuration."""

    print("=" * 60)
    print("🚀 Starting Comprehensive MCP Demo Server")
    print("=" * 60)
    print(f"Transport:     {config.transport_type.upper()}")
    print("=" * 60)
    print()
    print("Server is running and ready for connections...")
    print("Press Ctrl+C to stop the server")
    print("=" * 60)

    try:
        mcp.run(transport=config.transport_type)
    except KeyboardInterrupt:
        print("\n" + "=" * 60)
        print("🛑 Server stopped by user")
        print("=" * 60)
        sys.exit(0)
    except Exception as e:
        print(f"\n❌ Failed to start server: {e}")
        sys.exit(1)


def main():
    """Main function to run the MCP server with configurable transport."""
    args = parse_arguments()
    config = TransportConfig.from_args(args)

    start_server_with_transport(config)


if __name__ == "__main__":
    main()
