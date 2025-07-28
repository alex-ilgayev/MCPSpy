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

Run the server using FastMCP CLI:
    fastmcp run mcp_server.py                               # Default stdio transport
    fastmcp run mcp_server.py --transport http              # HTTP transport on default port 8000
    fastmcp run mcp_server.py --transport http --port 8080  # HTTP on custom port
    fastmcp run mcp_server.py --transport sse               # SSE transport
    fastmcp dev mcp_server.py                               # Development mode with MCP Inspector

    # With specific Python version
    fastmcp run mcp_server.py --python 3.11

    # With additional dependencies
    fastmcp run mcp_server.py --with pandas --with numpy

Run with uvicorn directly for streamable HTTP transport:
    uvicorn mcp_server:app --host 0.0.0.0 --port 8000
    uvicorn mcp_server:app --host 0.0.0.0 --port 8443 --ssl-keyfile tests/server.key --ssl-certfile tests/server.crt

Note: When using FastMCP CLI and uvicorn, it ignores the __main__ block and directly runs the server with the specified transport options.
"""

import asyncio
import json
import sys
from typing import List

from fastmcp import FastMCP, Context

# Initialize the MCP server
# Note: FastMCP 2.x doesn't support 'description' parameter in constructor
# The description can be set via server capabilities or documentation
mcp = FastMCP(
    name="Comprehensive MCP Demo Server",
    version="1.0.0",
)
app = mcp.http_app()


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


if __name__ == "__main__":
    """
    Direct execution support for stdio transport only.
    For HTTP-based transports, use fastmcp or uvicorn as documented above.
    """
    import argparse

    parser = argparse.ArgumentParser(description="MCP Server")
    parser.add_argument(
        "--transport",
        choices=["stdio"],
        default="stdio",
        help="Transport protocol (only stdio supported for direct execution)",
    )

    args = parser.parse_args()

    if args.transport != "stdio":
        print(
            f"Error: Transport '{args.transport}' not supported for direct execution."
        )
        print("Use fastmcp or uvicorn for HTTP-based transports:")
        print("  fastmcp run mcp_server.py --transport sse")
        print("  uvicorn mcp_server:app --host 0.0.0.0 --port 8000")
        sys.exit(1)

    # Run with stdio transport
    mcp.run()
