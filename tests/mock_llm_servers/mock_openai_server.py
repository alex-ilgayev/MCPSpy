#!/usr/bin/env python3
"""
Mock OpenAI API Server for testing MCPSpy LLM monitoring.

Implements:
- POST /v1/chat/completions (non-streaming and streaming)
- Proper SSE format for streaming responses

Usage:
    python mock_openai_server.py [--port PORT] [--ssl]
"""

import argparse
import json
import time
import uuid
from typing import Generator

from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse, JSONResponse
import uvicorn

app = FastAPI(title="Mock OpenAI API")


def generate_completion_id() -> str:
    """Generate a unique completion ID."""
    return f"chatcmpl-{uuid.uuid4().hex[:24]}"


def create_chat_response(
    model: str,
    content: str,
    finish_reason: str = "stop",
    prompt_tokens: int = 10,
    completion_tokens: int = 20,
) -> dict:
    """Create a non-streaming chat completion response."""
    return {
        "id": generate_completion_id(),
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {
                    "role": "assistant",
                    "content": content,
                },
                "finish_reason": finish_reason,
            }
        ],
        "usage": {
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": prompt_tokens + completion_tokens,
        },
        "system_fingerprint": "fp_mock_test",
    }


def create_tool_call_response(
    model: str,
    tool_name: str,
    tool_args: dict,
    prompt_tokens: int = 15,
    completion_tokens: int = 30,
) -> dict:
    """Create a response with tool calls."""
    return {
        "id": generate_completion_id(),
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {
                    "role": "assistant",
                    "content": None,
                    "tool_calls": [
                        {
                            "id": f"call_{uuid.uuid4().hex[:24]}",
                            "type": "function",
                            "function": {
                                "name": tool_name,
                                "arguments": json.dumps(tool_args),
                            },
                        }
                    ],
                },
                "finish_reason": "tool_calls",
            }
        ],
        "usage": {
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": prompt_tokens + completion_tokens,
        },
    }


def stream_chat_response(
    model: str,
    content: str,
    chunk_size: int = 5,
) -> Generator[str, None, None]:
    """Generate streaming chat completion chunks."""
    completion_id = generate_completion_id()
    created = int(time.time())

    # Initial chunk with role
    initial_chunk = {
        "id": completion_id,
        "object": "chat.completion.chunk",
        "created": created,
        "model": model,
        "choices": [
            {
                "index": 0,
                "delta": {"role": "assistant", "content": ""},
                "finish_reason": None,
            }
        ],
    }
    yield f"data: {json.dumps(initial_chunk)}\n\n"

    # Content chunks
    words = content.split()
    for i in range(0, len(words), chunk_size):
        chunk_words = words[i : i + chunk_size]
        chunk_text = " ".join(chunk_words)
        if i > 0:
            chunk_text = " " + chunk_text

        chunk = {
            "id": completion_id,
            "object": "chat.completion.chunk",
            "created": created,
            "model": model,
            "choices": [
                {
                    "index": 0,
                    "delta": {"content": chunk_text},
                    "finish_reason": None,
                }
            ],
        }
        yield f"data: {json.dumps(chunk)}\n\n"
        time.sleep(0.05)  # Simulate processing time

    # Final chunk with finish_reason
    final_chunk = {
        "id": completion_id,
        "object": "chat.completion.chunk",
        "created": created,
        "model": model,
        "choices": [
            {
                "index": 0,
                "delta": {},
                "finish_reason": "stop",
            }
        ],
    }
    yield f"data: {json.dumps(final_chunk)}\n\n"

    # Done marker
    yield "data: [DONE]\n\n"


@app.post("/v1/chat/completions")
async def chat_completions(request: Request):
    """Handle chat completion requests."""
    body = await request.json()

    model = body.get("model", "gpt-4")
    messages = body.get("messages", [])
    stream = body.get("stream", False)
    tools = body.get("tools", [])

    # Extract user message
    user_message = ""
    for msg in reversed(messages):
        if msg.get("role") == "user":
            content = msg.get("content", "")
            if isinstance(content, str):
                user_message = content
            elif isinstance(content, list):
                # Handle content array (multimodal)
                for part in content:
                    if isinstance(part, dict) and part.get("type") == "text":
                        user_message = part.get("text", "")
                        break
            break

    # Determine response based on user message
    if "tool" in user_message.lower() and tools:
        # Trigger tool call response
        response = create_tool_call_response(
            model=model,
            tool_name=tools[0]["function"]["name"] if tools else "get_weather",
            tool_args={"location": "San Francisco", "unit": "celsius"},
        )
        return JSONResponse(content=response)

    # Generate response content
    response_content = f"This is a mock response to: {user_message[:50]}"
    if "hello" in user_message.lower():
        response_content = "Hello! I'm a mock OpenAI API server for testing purposes."
    elif "weather" in user_message.lower():
        response_content = "The weather in San Francisco is sunny with a temperature of 22°C."
    elif "code" in user_message.lower():
        response_content = "Here's a simple Python function:\n\n```python\ndef hello():\n    return 'Hello, World!'\n```"

    if stream:
        return StreamingResponse(
            stream_chat_response(model, response_content),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
            },
        )
    else:
        response = create_chat_response(model=model, content=response_content)
        return JSONResponse(content=response)


@app.get("/health")
async def health():
    """Health check endpoint."""
    return {"status": "ok", "service": "mock-openai"}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Mock OpenAI API Server")
    parser.add_argument("--port", type=int, default=8001, help="Port to listen on")
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
