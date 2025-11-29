#!/usr/bin/env python3
"""
Test client for Anthropic Claude API - generates traffic for MCPSpy LLM monitoring tests.

Usage:
    python test_anthropic_client.py [--url URL] [--ssl-verify]
"""

import argparse
import json
import os
import httpx
import asyncio


async def test_non_streaming(client: httpx.AsyncClient, base_url: str):
    """Test non-streaming message."""
    print("Testing non-streaming message...")

    response = await client.post(
        f"{base_url}/v1/messages",
        json={
            "model": "claude-sonnet-4-5-20250929",
            "max_tokens": 1024,
            "messages": [
                {"role": "user", "content": "Hello, how are you?"},
            ],
            "system": "You are a helpful assistant.",
        },
        headers={
            "Content-Type": "application/json",
            "x-api-key": "sk-ant-test-key",
            "anthropic-version": "2023-06-01",
        },
    )

    print(f"  Status: {response.status_code}")
    data = response.json()
    if "content" in data and len(data["content"]) > 0:
        text = data["content"][0].get("text", "")
        print(f"  Response: {text[:50]}...")
    return response


async def test_streaming(client: httpx.AsyncClient, base_url: str):
    """Test streaming message."""
    print("Testing streaming message...")

    async with client.stream(
        "POST",
        f"{base_url}/v1/messages",
        json={
            "model": "claude-opus-4-1-20250805",
            "max_tokens": 1024,
            "stream": True,
            "messages": [
                {"role": "user", "content": "Tell me a short story."},
            ],
        },
        headers={
            "Content-Type": "application/json",
            "x-api-key": "sk-ant-test-key",
            "anthropic-version": "2023-06-01",
        },
    ) as response:
        print(f"  Status: {response.status_code}")
        content = ""
        async for line in response.aiter_lines():
            if line.startswith("data: "):
                data_str = line[6:]
                try:
                    event_data = json.loads(data_str)
                    if event_data.get("type") == "content_block_delta":
                        delta = event_data.get("delta", {})
                        if delta.get("type") == "text_delta":
                            content += delta.get("text", "")
                except json.JSONDecodeError:
                    pass
        print(f"  Response: {content[:50]}...")
    return response


async def test_tool_use(client: httpx.AsyncClient, base_url: str):
    """Test message with tool use."""
    print("Testing tool use...")

    response = await client.post(
        f"{base_url}/v1/messages",
        json={
            "model": "claude-sonnet-4-5-20250929",
            "max_tokens": 1024,
            "messages": [
                {"role": "user", "content": "What's the weather like? Use the tool."},
            ],
            "tools": [
                {
                    "name": "get_weather",
                    "description": "Get the current weather in a location",
                    "input_schema": {
                        "type": "object",
                        "properties": {
                            "location": {
                                "type": "string",
                                "description": "The city and country",
                            },
                            "unit": {
                                "type": "string",
                                "enum": ["celsius", "fahrenheit"],
                            },
                        },
                        "required": ["location"],
                    },
                }
            ],
        },
        headers={
            "Content-Type": "application/json",
            "x-api-key": "sk-ant-test-key",
            "anthropic-version": "2023-06-01",
        },
    )

    print(f"  Status: {response.status_code}")
    data = response.json()
    if "content" in data:
        for block in data["content"]:
            if block.get("type") == "tool_use":
                print(f"  Tool used: {block.get('name')}")
    return response


async def test_multimodal_text(client: httpx.AsyncClient, base_url: str):
    """Test multimodal message (text blocks)."""
    print("Testing multimodal text content...")

    response = await client.post(
        f"{base_url}/v1/messages",
        json={
            "model": "claude-sonnet-4-5-20250929",
            "max_tokens": 1024,
            "messages": [
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "Please analyze the following:"},
                        {"type": "text", "text": "The quick brown fox jumps over the lazy dog."},
                    ],
                },
            ],
        },
        headers={
            "Content-Type": "application/json",
            "x-api-key": "sk-ant-test-key",
            "anthropic-version": "2023-06-01",
        },
    )

    print(f"  Status: {response.status_code}")
    return response


async def test_conversation(client: httpx.AsyncClient, base_url: str):
    """Test multi-turn conversation."""
    print("Testing multi-turn conversation...")

    response = await client.post(
        f"{base_url}/v1/messages",
        json={
            "model": "claude-sonnet-4-5-20250929",
            "max_tokens": 1024,
            "messages": [
                {"role": "user", "content": "My name is Alice."},
                {"role": "assistant", "content": "Nice to meet you, Alice! How can I help you today?"},
                {"role": "user", "content": "What's my name?"},
            ],
        },
        headers={
            "Content-Type": "application/json",
            "x-api-key": "sk-ant-test-key",
            "anthropic-version": "2023-06-01",
        },
    )

    print(f"  Status: {response.status_code}")
    data = response.json()
    if "content" in data and len(data["content"]) > 0:
        text = data["content"][0].get("text", "")
        print(f"  Response: {text[:50]}...")
    return response


async def main():
    parser = argparse.ArgumentParser(description="Test Anthropic Claude API Client")
    parser.add_argument(
        "--url",
        type=str,
        default=os.environ.get("ANTHROPIC_API_URL", "https://127.0.0.1:8002"),
        help="Base URL for Anthropic API",
    )
    parser.add_argument(
        "--ssl-verify",
        action="store_true",
        help="Verify SSL certificates (disabled by default for testing)",
    )
    args = parser.parse_args()

    print(f"Testing Anthropic Claude API at {args.url}")
    print("=" * 50)

    client = httpx.AsyncClient(
        verify=args.ssl_verify,
        timeout=30.0,
    )

    try:
        await test_non_streaming(client, args.url)
        print()

        await test_streaming(client, args.url)
        print()

        await test_tool_use(client, args.url)
        print()

        await test_multimodal_text(client, args.url)
        print()

        await test_conversation(client, args.url)
        print()

        print("=" * 50)
        print("All tests completed!")

    finally:
        await client.aclose()


if __name__ == "__main__":
    asyncio.run(main())
