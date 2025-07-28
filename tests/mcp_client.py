#!/usr/bin/env python3
"""
MCP Message Type Simulator
==========================

This utility simulates MCP message types for eBPF monitoring validation.
It sends each message type once to validate parsing capabilities.

Usage:
    # stdio transport (requires server command)
    python mcp_client.py --server "python mcp_server.py"
    python mcp_client.py --transport stdio --server "python mcp_server.py"

    # HTTP-based transports (connect to existing server)
    python mcp_client.py --transport sse (default url: http://localhost:8000/sse)
    python mcp_client.py --transport streamable-http (default url: http://localhost:8000/mcp)
    python mcp_client.py --transport sse --url "http://localhost:8000/sse"
    python mcp_client.py --transport streamable-http --url "http://localhost:8000/mcp"
"""

import argparse
import asyncio
import logging
from typing import List, Optional, Dict, Any

from fastmcp import Client
from fastmcp.client.transports import SSETransport, PythonStdioTransport


class MCPMessageSimulator:
    """Simulates all MCP message types for validation using FastMCP."""

    def __init__(
        self,
        server_command: Optional[List[str]] = None,
        transport: str = "stdio",
        url: Optional[str] = None,
    ):
        """
        Initialize the MCP message simulator.

        Args:
            server_command: Command to start the MCP server (only used for stdio transport)
            transport: Transport layer to use ("stdio", "sse", "streamable-http")
            url: URL for HTTP-based transports (ignored for stdio)
        """
        self.server_command = server_command
        self.transport = transport
        self.url = url
        self.client: Optional[Client] = None

        # Validate transport-specific requirements
        if self.transport == "stdio" and not self.server_command:
            raise ValueError("Server command is required for stdio transport")

        # Set default URLs for HTTP-based transports
        if self.transport == "sse" and self.url is None:
            self.url = "http://localhost:8000/sse"
        elif self.transport == "streamable-http" and self.url is None:
            self.url = "http://localhost:8000/mcp"

        # Configure logging
        log_level = logging.INFO
        logging.basicConfig(
            level=log_level,
            format="%(asctime)s - %(levelname)s - %(message)s",
            datefmt="%H:%M:%S",
        )
        self.logger = logging.getLogger(__name__)

    def _create_client(self) -> Client:
        """Create a FastMCP client based on transport configuration."""
        if self.transport == "stdio":
            # For stdio, create PythonStdioTransport directly
            # Parse command to separate python executable and script path
            if len(self.server_command) < 2:
                raise ValueError(
                    "Server command must include both python executable and script path"
                )

            python_executable = self.server_command[0]
            script_path = self.server_command[1]
            script_args = (
                self.server_command[2:] if len(self.server_command) > 2 else None
            )

            transport = PythonStdioTransport(
                script_path=script_path, args=script_args, python_cmd=python_executable
            )
            return Client(transport)
        elif self.transport == "sse":
            # For SSE, use SSETransport explicitly
            transport = SSETransport(self.url)
            return Client(transport)
        elif self.transport == "streamable-http":
            # For streamable HTTP, pass the URL directly
            return Client(self.url)
        else:
            raise ValueError(f"Unsupported transport: {self.transport}")

    async def simulate_prompts(self) -> None:
        """Simulate prompt-related messages."""
        self.logger.info("=== Simulating Prompt Messages ===")

        try:
            # List prompts
            self.logger.info("Sending prompts/list request")
            prompts_response = await self.client.list_prompts()
            self.logger.info(f"Received {len(prompts_response.prompts)} prompts")

            # Get a specific prompt if available
            if prompts_response.prompts:
                prompt = prompts_response.prompts[0]
                self.logger.info(f"Sending prompts/get request for: {prompt.name}")

                # Use appropriate arguments based on prompt name
                if prompt.name == "code_review":
                    args = {
                        "code": "def hello():\n    print('Hello, World!')",
                        "language": "python",
                    }
                else:
                    # Generic args for unknown prompts
                    args = {"input": "test input"}

                _ = await self.client.get_prompt(prompt.name, args)
                self.logger.info("Received prompt response")
        except Exception as e:
            self.logger.error(f"Error simulating prompts: {e}")

    async def simulate_resources(self) -> None:
        """Simulate resource-related messages."""
        self.logger.info("=== Simulating Resource Messages ===")

        try:
            # List resources
            self.logger.info("Sending resources/list request")
            resources_response = await self.client.list_resources()
            self.logger.info(f"Received {len(resources_response.resources)} resources")

            # Read a resource if available
            if resources_response.resources:
                resource = resources_response.resources[0]
                self.logger.info(f"Sending resources/read request for: {resource.uri}")
                await self.client.read_resource(resource.uri)
                self.logger.info("Received resource content")
        except Exception as e:
            self.logger.error(f"Error simulating resources: {e}")

    async def simulate_tools(self) -> None:
        """Simulate tool-related messages."""
        self.logger.info("=== Simulating Tool Messages ===")

        try:
            # List tools
            self.logger.info("Sending tools/list request")
            tools_response = await self.client.list_tools()
            self.logger.info(f"Received {len(tools_response.tools)} tools")

            # Call the tools available, with specific arguments.
            for tool in tools_response.tools:
                self.logger.info(f"Sending tools/call request for: {tool.name}")

                # Use appropriate arguments based on tool name
                if tool.name == "get_weather":
                    args = {"city": "New York", "units": "metric"}
                elif tool.name == "process_data_with_progress":
                    args = {
                        "data": ["item1", "item2", "item3"],
                        "operation": "uppercase",
                    }
                else:
                    # Generic args for unknown tools
                    args = {"input": "test input"}

                await self.client.call_tool(tool.name, args)
                self.logger.info("Received tool call result")
        except Exception as e:
            self.logger.error(f"Error simulating tools: {e}")

    async def simulate_ping(self) -> None:
        """Simulate ping messages."""
        self.logger.info("=== Simulating Ping Messages ===")

        try:
            self.logger.info("Sending ping request")
            await self.client.ping()
            self.logger.info("Received ping response")
        except Exception as e:
            self.logger.error(f"Error simulating ping: {e}")

    async def _handle_sampling(
        self, messages: List[Dict[str, Any]], params: Optional[Dict[str, Any]] = None
    ) -> Dict[str, Any]:
        """Handle sampling requests from the server."""
        self.logger.info("Received sampling request from server")
        return {
            "role": "assistant",
            "content": "Sample response for message simulation.",
            "model": "simulator-model",
            "stopReason": "endTurn",
        }

    async def run_simulation(self) -> None:
        """Run the message simulation."""
        self.logger.info("Starting MCP message simulation")
        self.logger.info(f"Transport: {self.transport}")

        if self.transport == "stdio":
            self.logger.info(f"Server command: {' '.join(self.server_command)}")
        else:
            self.logger.info(f"Target URL: {self.url}")

        try:
            # Create client
            self.client = self._create_client()

            # Set sampling handler
            self.client.sampling_handler = self._handle_sampling

            # Connect and run simulation
            async with self.client:
                # Initialize connection is automatic in FastMCP
                self.logger.info("Connection initialized")

                # Simulate all message types
                await self.simulate_prompts()
                await self.simulate_resources()
                await self.simulate_tools()
                await self.simulate_ping()

                self.logger.info("Message simulation completed")

        except Exception as e:
            self.logger.error(f"Error during simulation: {e}")
            raise


async def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="MCP Message Simulator - Simulates all MCP message types for validation"
    )
    parser.add_argument(
        "--server",
        help="Command to start the MCP server (required for stdio transport)",
    )
    parser.add_argument(
        "--transport",
        choices=["stdio", "sse", "streamable-http"],
        default="stdio",
        help="Transport layer to use (default: stdio)",
    )
    parser.add_argument(
        "--url",
        help="URL for HTTP-based transports (default: http://localhost:8000/sse for SSE, http://localhost:8000/mcp for HTTP)",
    )

    args = parser.parse_args()

    # Parse server command if provided
    server_command = None
    if args.server:
        server_command = args.server.split()

    simulator = MCPMessageSimulator(
        server_command=server_command,
        transport=args.transport,
        url=args.url,
    )

    await simulator.run_simulation()


if __name__ == "__main__":
    asyncio.run(main())
