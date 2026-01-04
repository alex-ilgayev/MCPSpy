#!/usr/bin/env python3
"""
LLM API Client for E2E Testing - OpenAI
========================================

This utility makes API calls to OpenAI for eBPF monitoring validation.
It sends both streaming and non-streaming requests to validate LLM tracking capabilities.

Usage:
    # Non-streaming request only
    python llm_openai_client.py --mode non-streaming

    # Streaming request only
    python llm_openai_client.py --mode streaming

    # Both (default)
    python llm_openai_client.py

Requires:
    OPENAI_API_KEY environment variable
    openai package (pip install openai)
"""

import argparse
import logging
import os
import sys

from openai import OpenAI


MODEL_NAME = "gpt-4o-mini"


class OpenAIClient:
    """Simple OpenAI API client for e2e testing."""

    def __init__(self, api_key: str):
        """
        Initialize the OpenAI client.

        Args:
            api_key: OpenAI API key
        """
        self.client = OpenAI(api_key=api_key)

        # Configure logging
        logging.basicConfig(
            level=logging.INFO,
            format="%(asctime)s - %(levelname)s - %(message)s",
            datefmt="%H:%M:%S",
        )
        self.logger = logging.getLogger(__name__)

    def send_non_streaming_request(self, prompt: str) -> dict:
        """
        Send a non-streaming request to the OpenAI API.

        Args:
            prompt: The user prompt to send

        Returns:
            The API response
        """
        self.logger.info("=== Sending Non-Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        response = self.client.chat.completions.create(
            model=MODEL_NAME,
            messages=[{"role": "user", "content": prompt}],
            max_tokens=100,
        )

        if response.choices and response.choices[0].message.content:
            self.logger.info(f"Response preview: {response.choices[0].message.content[:100]}...")

        if response.usage:
            self.logger.info(
                f"Tokens - Prompt: {response.usage.prompt_tokens}, "
                f"Response: {response.usage.completion_tokens}, "
                f"Total: {response.usage.total_tokens}"
            )

        self.logger.info("Non-streaming request completed")
        return response

    def send_streaming_request(self, prompt: str) -> str:
        """
        Send a streaming request to the OpenAI API.

        Args:
            prompt: The user prompt to send

        Returns:
            The complete response text
        """
        self.logger.info("=== Sending Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        full_response = ""
        chunk_count = 0

        stream = self.client.chat.completions.create(
            model=MODEL_NAME,
            messages=[{"role": "user", "content": prompt}],
            max_tokens=100,
            stream=True,
        )

        for chunk in stream:
            chunk_count += 1
            if chunk.choices and chunk.choices[0].delta.content:
                full_response += chunk.choices[0].delta.content

        self.logger.info(f"Total chunks received: {chunk_count}")
        self.logger.info(f"Response preview: {full_response[:100]}...")
        self.logger.info("Streaming request completed")

        return full_response

    def run_all_tests(self, mode: str = "both") -> None:
        """
        Run the LLM API tests.

        Args:
            mode: Test mode - "streaming", "non-streaming", or "both"
        """
        self.logger.info("Starting OpenAI LLM API E2E test")
        self.logger.info(f"Mode: {mode}")
        self.logger.info(f"Model: {MODEL_NAME}")

        try:
            if mode in ("non-streaming", "both"):
                self.send_non_streaming_request("Repeat exactly: PING")

            if mode in ("streaming", "both"):
                self.send_streaming_request("Repeat exactly: PONG")

            self.logger.info("OpenAI LLM API E2E test completed successfully")

        except Exception as e:
            self.logger.error(f"Error during test: {e}")
            raise


def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="OpenAI LLM API Client - Makes OpenAI API calls for E2E testing"
    )
    parser.add_argument(
        "--mode",
        choices=["streaming", "non-streaming", "both"],
        default="both",
        help="Test mode: streaming, non-streaming, or both (default: both)",
    )

    args = parser.parse_args()

    # Get API key from environment
    api_key = os.environ.get("OPENAI_API_KEY")
    if not api_key:
        print(
            "ERROR: OPENAI_API_KEY environment variable is required", file=sys.stderr
        )
        sys.exit(1)

    client = OpenAIClient(api_key)
    client.run_all_tests(mode=args.mode)


if __name__ == "__main__":
    main()
