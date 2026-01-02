#!/usr/bin/env python3
"""
LLM API Client for E2E Testing
==============================

This utility makes API calls to LLM providers (Anthropic) for eBPF monitoring validation.
It sends both streaming and non-streaming requests to validate LLM tracking capabilities.

Usage:
    # Non-streaming request only
    python llm_client.py --mode non-streaming

    # Streaming request only
    python llm_client.py --mode streaming

    # Both (default)
    python llm_client.py

Requires:
    CLAUDE_CODE_OAUTH_TOKEN environment variable
"""

import argparse
import asyncio
import logging
import os
import sys

import httpx


class AnthropicClient:
    """Simple Anthropic API client for e2e testing."""

    BASE_URL = "https://api.anthropic.com"
    MESSAGES_ENDPOINT = "/v1/messages?beta=true"

    def __init__(self, api_key: str):
        """
        Initialize the Anthropic client.

        Args:
            api_key: Claude Code OAuth API key
        """
        self.api_key = api_key
        self.headers = {
            "user-agent": "claude-cli/2.0.60 (external, cli)",
            "accept-language": "*",
            "authorization": f"Bearer {api_key}",
            # "x-api-key": api_key,
            "anthropic-version": "2023-06-01",
            "content-type": "application/json",
            "accept": "application/json",
            "anthropic-beta": "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,tool-examples-2025-10-29",
        }

        # Configure logging
        logging.basicConfig(
            level=logging.INFO,
            format="%(asctime)s - %(levelname)s - %(message)s",
            datefmt="%H:%M:%S",
        )
        self.logger = logging.getLogger(__name__)

    async def send_non_streaming_request(self, prompt: str) -> dict:
        """
        Send a non-streaming request to the Anthropic API.

        Args:
            prompt: The user prompt to send

        Returns:
            The API response as a dict
        """
        self.logger.info("=== Sending Non-Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        payload = {
            "model": "claude-haiku-4-5-20251001",
            "max_tokens": 100,
            "messages": [{"role": "user", "content": prompt}],
        }

        async with httpx.AsyncClient(timeout=60.0) as client:
            response = await client.post(
                f"{self.BASE_URL}{self.MESSAGES_ENDPOINT}",
                headers=self.headers,
                json=payload,
            )
            response.raise_for_status()
            result = response.json()

        self.logger.info(f"Response type: {result.get('type')}")
        self.logger.info(f"Model: {result.get('model')}")
        if result.get("content"):
            content_text = result["content"][0].get("text", "")[:100]
            self.logger.info(f"Content preview: {content_text}...")
        self.logger.info("Non-streaming request completed")

        return result

    async def send_streaming_request(self, prompt: str) -> str:
        """
        Send a streaming request to the Anthropic API.

        Args:
            prompt: The user prompt to send

        Returns:
            The complete response text
        """
        self.logger.info("=== Sending Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        payload = {
            "model": "claude-haiku-4-5-20251001",
            "max_tokens": 100,
            "stream": True,
            "messages": [{"role": "user", "content": prompt}],
        }

        full_response = ""
        event_count = 0

        async with httpx.AsyncClient(timeout=60.0) as client:
            async with client.stream(
                "POST",
                f"{self.BASE_URL}{self.MESSAGES_ENDPOINT}",
                headers=self.headers,
                json=payload,
            ) as response:
                response.raise_for_status()

                async for line in response.aiter_lines():
                    if not line:
                        continue

                    # Parse SSE format
                    if line.startswith("data: "):
                        data = line[6:]  # Remove "data: " prefix
                        event_count += 1

                        # Parse JSON to extract text deltas
                        try:
                            import json

                            event = json.loads(data)
                            event_type = event.get("type", "")

                            if event_type == "content_block_delta":
                                delta = event.get("delta", {})
                                if delta.get("type") == "text_delta":
                                    text = delta.get("text", "")
                                    full_response += text

                            elif event_type == "message_start":
                                msg = event.get("message", {})
                                self.logger.info(
                                    f"Stream started - Model: {msg.get('model')}"
                                )

                            elif event_type == "message_stop":
                                self.logger.info("Stream completed")

                        except json.JSONDecodeError:
                            pass  # Skip non-JSON lines

        self.logger.info(f"Total SSE events received: {event_count}")
        self.logger.info(f"Response preview: {full_response[:100]}...")
        self.logger.info("Streaming request completed")

        return full_response

    async def run_all_tests(self, mode: str = "both") -> None:
        """
        Run the LLM API tests.

        Args:
            mode: Test mode - "streaming", "non-streaming", or "both"
        """
        self.logger.info("Starting LLM API E2E test")
        self.logger.info(f"Mode: {mode}")
        self.logger.info(f"Target: {self.BASE_URL}")

        try:
            if mode in ("non-streaming", "both"):
                await self.send_non_streaming_request("Repeat exactly: PING")

            if mode in ("streaming", "both"):
                await self.send_streaming_request("Repeat exactly: PONG")

            self.logger.info("LLM API E2E test completed successfully")

        except httpx.HTTPStatusError as e:
            self.logger.error(
                f"HTTP error: {e.response.status_code} - {e.response.text}"
            )
            raise
        except Exception as e:
            self.logger.error(f"Error during test: {e}")
            raise


async def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="LLM API Client - Makes Anthropic API calls for E2E testing"
    )
    parser.add_argument(
        "--mode",
        choices=["streaming", "non-streaming", "both"],
        default="both",
        help="Test mode: streaming, non-streaming, or both (default: both)",
    )

    args = parser.parse_args()

    # Get API key from environment
    api_key = os.environ.get("CLAUDE_CODE_OAUTH_TOKEN")
    if not api_key:
        print(
            "ERROR: CLAUDE_CODE_OAUTH_TOKEN environment variable is required", file=sys.stderr
        )
        sys.exit(1)

    client = AnthropicClient(api_key)
    await client.run_all_tests(mode=args.mode)


if __name__ == "__main__":
    asyncio.run(main())
