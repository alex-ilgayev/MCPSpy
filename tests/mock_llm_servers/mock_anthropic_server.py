#!/usr/bin/env python3
"""
Mock Anthropic Claude API Server for testing MCPSpy LLM monitoring.

Implements:
- POST /v1/messages (non-streaming and streaming)
- Proper SSE event format with message_start, content_block_*, message_delta, message_stop

Usage:
    python mock_anthropic_server.py [--port PORT] [--ssl]
"""

import argparse
import json
import time
import uuid
from typing import Generator

from fastapi import FastAPI, Request, Header
from fastapi.responses import StreamingResponse, JSONResponse
import uvicorn

app = FastAPI(title="Mock Anthropic Claude API")


def generate_message_id() -> str:
    """Generate a unique message ID."""
    return f"msg_{uuid.uuid4().hex[:24]}"


def create_message_response(
    model: str,
    content: str,
    stop_reason: str = "end_turn",
    input_tokens: int = 10,
    output_tokens: int = 20,
) -> dict:
    """Create a non-streaming message response."""
    return {
        "id": generate_message_id(),
        "type": "message",
        "role": "assistant",
        "content": [
            {
                "type": "text",
                "text": content,
            }
        ],
        "model": model,
        "stop_reason": stop_reason,
        "stop_sequence": None,
        "usage": {
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
        },
    }


def create_tool_use_response(
    model: str,
    tool_name: str,
    tool_input: dict,
    input_tokens: int = 15,
    output_tokens: int = 30,
) -> dict:
    """Create a response with tool use."""
    return {
        "id": generate_message_id(),
        "type": "message",
        "role": "assistant",
        "content": [
            {
                "type": "tool_use",
                "id": f"toolu_{uuid.uuid4().hex[:24]}",
                "name": tool_name,
                "input": tool_input,
            }
        ],
        "model": model,
        "stop_reason": "tool_use",
        "stop_sequence": None,
        "usage": {
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
        },
    }


def stream_message_response(
    model: str,
    content: str,
    input_tokens: int = 10,
) -> Generator[str, None, None]:
    """Generate streaming message response with Anthropic SSE format."""
    message_id = generate_message_id()

    # message_start event
    message_start = {
        "type": "message_start",
        "message": {
            "id": message_id,
            "type": "message",
            "role": "assistant",
            "content": [],
            "model": model,
            "stop_reason": None,
            "stop_sequence": None,
            "usage": {
                "input_tokens": input_tokens,
                "output_tokens": 1,
            },
        },
    }
    yield f"event: message_start\ndata: {json.dumps(message_start)}\n\n"

    # content_block_start event
    content_block_start = {
        "type": "content_block_start",
        "index": 0,
        "content_block": {
            "type": "text",
            "text": "",
        },
    }
    yield f"event: content_block_start\ndata: {json.dumps(content_block_start)}\n\n"

    # ping event (optional)
    yield f"event: ping\ndata: {json.dumps({'type': 'ping'})}\n\n"

    # content_block_delta events - split content into chunks
    words = content.split()
    output_tokens = 0
    for i in range(0, len(words), 3):
        chunk_words = words[i : i + 3]
        chunk_text = " ".join(chunk_words)
        if i > 0:
            chunk_text = " " + chunk_text

        delta = {
            "type": "content_block_delta",
            "index": 0,
            "delta": {
                "type": "text_delta",
                "text": chunk_text,
            },
        }
        yield f"event: content_block_delta\ndata: {json.dumps(delta)}\n\n"
        output_tokens += len(chunk_words)
        time.sleep(0.05)

    # content_block_stop event
    content_block_stop = {
        "type": "content_block_stop",
        "index": 0,
    }
    yield f"event: content_block_stop\ndata: {json.dumps(content_block_stop)}\n\n"

    # message_delta event
    message_delta = {
        "type": "message_delta",
        "delta": {
            "stop_reason": "end_turn",
            "stop_sequence": None,
        },
        "usage": {
            "output_tokens": output_tokens,
        },
    }
    yield f"event: message_delta\ndata: {json.dumps(message_delta)}\n\n"

    # message_stop event
    message_stop = {
        "type": "message_stop",
    }
    yield f"event: message_stop\ndata: {json.dumps(message_stop)}\n\n"


@app.post("/v1/messages")
async def messages(
    request: Request,
    x_api_key: str = Header(None, alias="x-api-key"),
    anthropic_version: str = Header(None, alias="anthropic-version"),
):
    """Handle messages requests."""
    body = await request.json()

    model = body.get("model", "claude-sonnet-4-5-20250929")
    messages = body.get("messages", [])
    stream = body.get("stream", False)
    tools = body.get("tools", [])
    system = body.get("system", "")

    # Extract user message
    user_message = ""
    for msg in reversed(messages):
        if msg.get("role") == "user":
            content = msg.get("content", "")
            if isinstance(content, str):
                user_message = content
            elif isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get("type") == "text":
                        user_message = block.get("text", "")
                        break
            break

    # Determine response based on user message
    if "tool" in user_message.lower() and tools:
        response = create_tool_use_response(
            model=model,
            tool_name=tools[0]["name"] if tools else "get_weather",
            tool_input={"location": "San Francisco", "unit": "celsius"},
        )
        return JSONResponse(content=response)

    # Generate response content
    response_content = f"This is a mock Claude response to: {user_message[:50]}"
    if "hello" in user_message.lower():
        response_content = "Hello! I'm a mock Claude API server for testing purposes."
    elif "weather" in user_message.lower():
        response_content = "Based on my knowledge, San Francisco typically has mild weather. However, I'd recommend checking a weather service for current conditions."
    elif "code" in user_message.lower():
        response_content = "Here's a Python example:\n\n```python\ndef greet(name: str) -> str:\n    return f'Hello, {name}!'\n\nprint(greet('World'))\n```"

    if stream:
        return StreamingResponse(
            stream_message_response(model, response_content),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
            },
        )
    else:
        response = create_message_response(model=model, content=response_content)
        return JSONResponse(content=response)


@app.get("/health")
async def health():
    """Health check endpoint."""
    return {"status": "ok", "service": "mock-anthropic"}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Mock Anthropic Claude API Server")
    parser.add_argument("--port", type=int, default=8002, help="Port to listen on")
    parser.add_argument("--host", type=str, default="127.0.0.1", help="Host to bind to")
    parser.add_argument("--ssl", action="store_true", help="Enable SSL")
    args = parser.parse_args()

    ssl_kwargs = {}
    if args.ssl:
        ssl_kwargs = {
            "ssl_keyfile": "tests/server.key",
            "ssl_certfile": "tests/server.crt",
        }

    uvicorn.run(app, host=args.host, port=args.port, **ssl_kwargs)
