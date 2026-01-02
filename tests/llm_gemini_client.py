#!/usr/bin/env python3
"""
LLM API Client for E2E Testing - Gemini
=======================================

This utility makes API calls to Google Gemini for eBPF monitoring validation.
It sends both streaming and non-streaming requests to validate LLM tracking capabilities.

Usage:
    # Non-streaming request only
    python llm_gemini_client.py --mode non-streaming

    # Streaming request only
    python llm_gemini_client.py --mode streaming

    # Both (default)
    python llm_gemini_client.py

Requires:
    GEMINI_API_KEY environment variable
    google-genai package (pip install google-genai)
"""

import argparse
import logging
import os
import sys

from google import genai
from google.genai import types


MODEL_NAME = "gemini-2.0-flash"


class GeminiClient:
    """Simple Gemini API client for e2e testing."""

    def __init__(self, api_key: str):
        """
        Initialize the Gemini client.

        Args:
            api_key: Gemini API key
        """
        self.client = genai.Client(api_key=api_key)

        # Configure logging
        logging.basicConfig(
            level=logging.INFO,
            format="%(asctime)s - %(levelname)s - %(message)s",
            datefmt="%H:%M:%S",
        )
        self.logger = logging.getLogger(__name__)

    def send_non_streaming_request(self, prompt: str) -> dict:
        """
        Send a non-streaming request to the Gemini API.

        Args:
            prompt: The user prompt to send

        Returns:
            The API response
        """
        self.logger.info("=== Sending Non-Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        response = self.client.models.generate_content(
            model=MODEL_NAME,
            contents=prompt,
            config=types.GenerateContentConfig(
                max_output_tokens=100,
            ),
        )

        if response.text:
            self.logger.info(f"Response preview: {response.text[:100]}...")

        if response.usage_metadata:
            self.logger.info(
                f"Tokens - Prompt: {response.usage_metadata.prompt_token_count}, "
                f"Response: {response.usage_metadata.candidates_token_count}, "
                f"Total: {response.usage_metadata.total_token_count}"
            )

        self.logger.info("Non-streaming request completed")
        return response

    def send_streaming_request(self, prompt: str) -> str:
        """
        Send a streaming request to the Gemini API.

        Args:
            prompt: The user prompt to send

        Returns:
            The complete response text
        """
        self.logger.info("=== Sending Streaming Request ===")
        self.logger.info(f"Prompt: {prompt[:50]}...")

        full_response = ""
        chunk_count = 0

        for chunk in self.client.models.generate_content_stream(
            model=MODEL_NAME,
            contents=prompt,
            config=types.GenerateContentConfig(
                max_output_tokens=100,
            ),
        ):
            chunk_count += 1
            if chunk.text:
                full_response += chunk.text

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
        self.logger.info("Starting Gemini LLM API E2E test")
        self.logger.info(f"Mode: {mode}")
        self.logger.info(f"Model: {MODEL_NAME}")

        try:
            if mode in ("non-streaming", "both"):
                self.send_non_streaming_request("Repeat exactly: PING")

            if mode in ("streaming", "both"):
                self.send_streaming_request("Repeat exactly: PONG")

            self.logger.info("Gemini LLM API E2E test completed successfully")

        except Exception as e:
            self.logger.error(f"Error during test: {e}")
            raise


def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="Gemini LLM API Client - Makes Gemini API calls for E2E testing"
    )
    parser.add_argument(
        "--mode",
        choices=["streaming", "non-streaming", "both"],
        default="both",
        help="Test mode: streaming, non-streaming, or both (default: both)",
    )

    args = parser.parse_args()

    # Get API key from environment
    api_key = os.environ.get("GEMINI_API_KEY")
    if not api_key:
        print(
            "ERROR: GEMINI_API_KEY environment variable is required", file=sys.stderr
        )
        sys.exit(1)

    client = GeminiClient(api_key)
    client.run_all_tests(mode=args.mode)


if __name__ == "__main__":
    main()
