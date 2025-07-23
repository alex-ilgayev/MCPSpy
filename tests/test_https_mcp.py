#!/usr/bin/env python3

import asyncio
import json
import ssl
import aiohttp
from aiohttp import web
import time
import argparse
import os


# Simple HTTPS MCP server that responds to MCP requests
class MCPHTTPSServer:
    def __init__(self, port=8443):
        self.port = port

    async def handle_mcp(self, request):
        """Handle MCP requests"""
        try:
            # Read the JSON-RPC request
            data = await request.json()
            print(f"Server received: {json.dumps(data, indent=2)}")

            # Handle different MCP methods
            if data.get("method") == "initialize":
                response = {
                    "jsonrpc": "2.0",
                    "id": data.get("id"),
                    "result": {
                        "protocolVersion": "2025-06-18",
                        "capabilities": {
                            "tools": {"listChanged": True},
                            "resources": {"subscribe": True},
                        },
                        "serverInfo": {"name": "test-https-server", "version": "1.0.0"},
                    },
                }
            elif data.get("method") == "tools/list":
                response = {
                    "jsonrpc": "2.0",
                    "id": data.get("id"),
                    "result": {
                        "tools": [
                            {
                                "name": "test_tool",
                                "description": "A test tool",
                                "inputSchema": {
                                    "type": "object",
                                    "properties": {"input": {"type": "string"}},
                                },
                            }
                        ]
                    },
                }
            elif data.get("method") == "tools/call":
                response = {
                    "jsonrpc": "2.0",
                    "id": data.get("id"),
                    "result": {
                        "content": [
                            {
                                "type": "text",
                                "text": f"Tool executed with input: {data.get('params', {}).get('arguments', {}).get('input', 'no input')}",
                            }
                        ]
                    },
                }
            else:
                response = {
                    "jsonrpc": "2.0",
                    "id": data.get("id"),
                    "error": {"code": -32601, "message": "Method not found"},
                }

            print(f"Server sending: {json.dumps(response, indent=2)}")
            return web.json_response(response)

        except Exception as e:
            print(f"Server error: {e}")
            return web.json_response(
                {
                    "jsonrpc": "2.0",
                    "id": None,
                    "error": {"code": -32603, "message": str(e)},
                }
            )

    async def start(self):
        """Start the HTTPS server"""
        app = web.Application()
        app.router.add_post("/mcp", self.handle_mcp)

        # Create self-signed certificate context
        ssl_context = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)

        # Use existing certificates if available, otherwise create temporary ones
        cert_file = "test_cert.pem"
        key_file = "test_key.pem"

        if not os.path.exists(cert_file) or not os.path.exists(key_file):
            # Generate self-signed certificate
            os.system(
                f'openssl req -x509 -newkey rsa:2048 -keyout {key_file} -out {cert_file} -days 1 -nodes -subj "/CN=localhost"'
            )

        ssl_context.load_cert_chain(cert_file, key_file)

        runner = web.AppRunner(app)
        await runner.setup()
        site = web.TCPSite(runner, "localhost", self.port, ssl_context=ssl_context)
        await site.start()

        print(f"HTTPS MCP server running on https://localhost:{self.port}/mcp")

        # Keep server running
        try:
            await asyncio.Event().wait()
        except KeyboardInterrupt:
            pass
        finally:
            await runner.cleanup()


# Simple HTTPS MCP client
class MCPHTTPSClient:
    def __init__(self, server_url="https://localhost:8443/mcp"):
        self.server_url = server_url
        self.request_id = 0

    async def send_request(self, method, params=None):
        """Send a JSON-RPC request"""
        self.request_id += 1
        request = {
            "jsonrpc": "2.0",
            "id": self.request_id,
            "method": method,
            "params": params or {},
        }

        print(f"Client sending: {json.dumps(request, indent=2)}")

        # Create SSL context that doesn't verify certificates (for testing)
        ssl_context = ssl.create_default_context()
        ssl_context.check_hostname = False
        ssl_context.verify_mode = ssl.CERT_NONE

        async with aiohttp.ClientSession() as session:
            async with session.post(
                self.server_url,
                json=request,
                ssl=ssl_context,
                headers={"Content-Type": "application/json"},
            ) as response:
                result = await response.json()
                print(f"Client received: {json.dumps(result, indent=2)}")
                return result

    async def test_sequence(self):
        """Run a test sequence of MCP calls"""
        # Initialize
        await self.send_request(
            "initialize",
            {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "test-https-client", "version": "1.0.0"},
            },
        )

        await asyncio.sleep(0.5)

        # List tools
        await self.send_request("tools/list")

        await asyncio.sleep(0.5)

        # Call a tool
        await self.send_request(
            "tools/call",
            {"name": "test_tool", "arguments": {"input": "Hello from HTTPS!"}},
        )

        await asyncio.sleep(0.5)

        # Send notification (no ID)
        notification = {
            "jsonrpc": "2.0",
            "method": "notifications/progress",
            "params": {"progress": 50},
        }
        print(f"Client sending notification: {json.dumps(notification, indent=2)}")

        ssl_context = ssl.create_default_context()
        ssl_context.check_hostname = False
        ssl_context.verify_mode = ssl.CERT_NONE

        async with aiohttp.ClientSession() as session:
            async with session.post(
                self.server_url,
                json=notification,
                ssl=ssl_context,
                headers={"Content-Type": "application/json"},
            ):
                pass  # Notifications don't expect responses


async def main():
    parser = argparse.ArgumentParser(description="Test HTTPS MCP communication")
    parser.add_argument(
        "--mode",
        choices=["server", "client", "both"],
        default="both",
        help="Run as server, client, or both",
    )
    parser.add_argument("--port", type=int, default=8443, help="HTTPS port to use")

    args = parser.parse_args()

    if args.mode == "server":
        server = MCPHTTPSServer(args.port)
        await server.start()
    elif args.mode == "client":
        client = MCPHTTPSClient(f"https://localhost:{args.port}/mcp")
        await client.test_sequence()
    else:  # both
        # Start server in background
        server = MCPHTTPSServer(args.port)
        server_task = asyncio.create_task(server.start())

        # Wait for server to start
        await asyncio.sleep(2)

        # Run client
        client = MCPHTTPSClient(f"https://localhost:{args.port}/mcp")
        await client.test_sequence()

        # Keep running for a bit to see any additional traffic
        await asyncio.sleep(2)

        # Cancel server
        server_task.cancel()
        try:
            await server_task
        except asyncio.CancelledError:
            pass


if __name__ == "__main__":
    asyncio.run(main())
