#!/usr/bin/env python3
"""
Test client for OpenAI API - generates traffic for MCPSpy LLM monitoring tests.

Usage:
    python test_openai_client.py [--url URL] [--ssl-verify]
"""

import argparse
import json
import os
import ssl
import httpx
import asyncio


async def test_non_streaming(client: httpx.AsyncClient, base_url: str):
    """Test non-streaming chat completion."""
    print("Testing non-streaming completion...")

    response = await client.post(
        f"{base_url}/v1/chat/completions",
        json={
            "model": "gpt-4",
            "messages": [
                {"role": "system", "content": "You are a helpful assistant."},
                {"role": "user", "content": "Hello, how are you?"},
            ],
            "stream": False,
            "temperature": 0.7,
            "max_tokens": 100,
        },
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer sk-test-key",
        },
    )

    print(f"  Status: {response.status_code}")
    data = response.json()
    if "choices" in data:
        print(f"  Response: {data['choices'][0]['message']['content'][:50]}...")
    return response


async def test_streaming(client: httpx.AsyncClient, base_url: str):
    """Test streaming chat completion."""
    print("Testing streaming completion...")

    async with client.stream(
        "POST",
        f"{base_url}/v1/chat/completions",
        json={
            "model": "gpt-4-turbo",
            "messages": [
                {"role": "user", "content": "Tell me a short joke."},
            ],
            "stream": True,
            "temperature": 0.8,
        },
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer sk-test-key",
        },
    ) as response:
        print(f"  Status: {response.status_code}")
        content = ""
        async for line in response.aiter_lines():
            if line.startswith("data: "):
                data_str = line[6:]
                if data_str == "[DONE]":
                    break
                try:
                    chunk = json.loads(data_str)
                    if chunk["choices"][0]["delta"].get("content"):
                        content += chunk["choices"][0]["delta"]["content"]
                except json.JSONDecodeError:
                    pass
        print(f"  Response: {content[:50]}...")
    return response


async def test_tool_calling(client: httpx.AsyncClient, base_url: str):
    """Test chat completion with tool calling."""
    print("Testing tool calling...")

    response = await client.post(
        f"{base_url}/v1/chat/completions",
        json={
            "model": "gpt-4",
            "messages": [
                {"role": "user", "content": "What's the weather like? Use the tool."},
            ],
            "tools": [
                {
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        "description": "Get the current weather in a location",
                        "parameters": {
                            "type": "object",
                            "properties": {
                                "location": {"type": "string"},
                                "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
                            },
                            "required": ["location"],
                        },
                    },
                }
            ],
            "tool_choice": "auto",
        },
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer sk-test-key",
        },
    )

    print(f"  Status: {response.status_code}")
    data = response.json()
    if "choices" in data and data["choices"][0]["message"].get("tool_calls"):
        tool_call = data["choices"][0]["message"]["tool_calls"][0]
        print(f"  Tool called: {tool_call['function']['name']}")
    return response


async def test_multimodal(client: httpx.AsyncClient, base_url: str):
    """Test multimodal (text) request."""
    print("Testing multimodal content...")

    response = await client.post(
        f"{base_url}/v1/chat/completions",
        json={
            "model": "gpt-4-vision-preview",
            "messages": [
                {
                    "role": "user",
                    "content": [
                        {"type": "text", "text": "What do you see in this image?"},
                        # Note: In a real test, we'd include an image here
                        {"type": "text", "text": "Describe anything you observe."},
                    ],
                },
            ],
            "max_tokens": 300,
        },
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer sk-test-key",
        },
    )

    print(f"  Status: {response.status_code}")
    return response


async def main():
    parser = argparse.ArgumentParser(description="Test OpenAI API Client")
    parser.add_argument(
        "--url",
        type=str,
        default=os.environ.get("OPENAI_API_URL", "https://127.0.0.1:8001"),
        help="Base URL for OpenAI API",
    )
    parser.add_argument(
        "--ssl-verify",
        action="store_true",
        help="Verify SSL certificates (disabled by default for testing)",
    )
    args = parser.parse_args()

    print(f"Testing OpenAI API at {args.url}")
    print("=" * 50)

    # Create HTTP client with or without SSL verification
    client = httpx.AsyncClient(
        verify=args.ssl_verify,
        timeout=30.0,
    )

    try:
        # Run tests
        await test_non_streaming(client, args.url)
        print()

        await test_streaming(client, args.url)
        print()

        await test_tool_calling(client, args.url)
        print()

        await test_multimodal(client, args.url)
        print()

        print("=" * 50)
        print("All tests completed!")

    finally:
        await client.aclose()


if __name__ == "__main__":
    asyncio.run(main())
