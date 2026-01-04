# MCPSpy Tests Guide

## Quick Start

### Run All Tests

```bash
make test-e2e
```

### Run Specific Transport

```bash
make test-e2e-stdio      # Stdio transport
make test-e2e-https      # HTTPS transport
```

### Run Scenario Only (no MCPSpy)

```bash
make test-scenario-stdio          # Stdio transport
make test-scenario-https          # HTTPS transport
make test-scenario-security       # Security/injection test
make test-scenario-llm-anthropic  # Anthropic LLM API test
make test-scenario-llm-gemini     # Gemini LLM API test
```

### Run Security/Prompt Injection E2E Test

Requires `HF_TOKEN` environment variable:

```bash
HF_TOKEN=your_huggingface_token make test-e2e-security
```

### Run LLM API Monitoring E2E Tests

**Anthropic Claude API** (requires `CLAUDE_CODE_OAUTH_TOKEN`):

```bash
CLAUDE_CODE_OAUTH_TOKEN=your_api_key make test-e2e-llm-anthropic
```

**Google Gemini API** (requires `GEMINI_API_KEY`):

```bash
GEMINI_API_KEY=your_api_key make test-e2e-llm-gemini
```

### Update Expected Outputs

```bash
make test-update-all              # All scenarios
make test-update-stdio            # Specific scenario
make test-update-https
make test-update-llm-anthropic
make test-update-llm-gemini
```

---

## e2e_test.py CLI Utility

The `e2e_test.py` is the core test runner. It loads YAML configuration and executes test scenarios.

### Basic Usage

```bash
# Run all scenarios from config
python tests/e2e_test.py --config tests/e2e_config.yaml

# Run specific scenario
python tests/e2e_test.py --config tests/e2e_config.yaml --scenario stdio-fastmcp

# Update expected output for a scenario
python tests/e2e_test.py --config tests/e2e_config.yaml --scenario http-fastmcp --update-expected

# Enable verbose output
python tests/e2e_test.py --config tests/e2e_config.yaml --verbose
```

### Command-Line Arguments

| Argument            | Type   | Required | Description                                                                                                           |
| ------------------- | ------ | -------- | --------------------------------------------------------------------------------------------------------------------- |
| `--config`          | Path   | Yes      | Path to YAML configuration file                                                                                       |
| `--scenario`        | String | No       | Run specific scenario by name (default: all scenarios)                                                                |
| `--update-expected` | Flag   | No       | Update expected output files instead of validating                                                                    |
| `--verbose`, `-v`   | Flag   | No       | Enable verbose logging output                                                                                         |
| `--skip-mcpspy`     | Flag   | No       | Skip MCPSpy monitoring - only run traffic generation and pre/post commands (useful for debugging MCP implementations) |

---

## Test Scenarios

### stdio-fastmcp

**What it tests:**

- Direct subprocess communication via stdio
- All MCP message types (tools, resources, prompts, ping)
- Request/response pairing

### http-fastmcp

**What it tests:**

- HTTPS transport with self-signed certificates
- All MCP message types over HTTP
- StreamableHTTP protocol handling

### security-injection

**What it tests:**

- Prompt injection detection via HuggingFace API
- Security alert event generation for malicious content
- Benign vs malicious content classification
- Integration of security analyzer with MCP event processing

**Requirements:**

- `HF_TOKEN` environment variable with valid HuggingFace API token

### llm-anthropic

**What it tests:**

- LLM API monitoring with Anthropic Claude API
- Non-streaming API request/response capture
- Streaming (SSE) API request/response capture
- Model and content extraction from API calls

**Requirements:**

- `CLAUDE_CODE_OAUTH_TOKEN` environment variable with valid Claude Code OAuth API key

### claude-code

**What it tests:**

- MCP server initialization (filesystem and deepwiki HTTP servers)
- Claude Code tool usage (Read, Bash tools)
- LLM API calls (Anthropic API requests/responses)
- Predictable tool execution with known inputs/outputs

**Test data:**

- `tests/test_tool_data.txt` - Test file with known content for Read tool testing
- `tests/claude_config.json` - MCP server configuration

## File Reference

| File                                  | Purpose                                                  |
| ------------------------------------- | -------------------------------------------------------- |
| `e2e_test.py`                         | Main test runner - loads config and executes scenarios   |
| `e2e_config.yaml`                     | Test scenario definitions and validation rules           |
| `e2e_config_schema.py`                | Pydantic schema for config validation                    |
| `mcp_server.py`                       | FastMCP test server (stdio/http/sse transports)          |
| `mcp_client.py`                       | MCP client that generates test traffic                   |
| `llm_client.py`                       | LLM API client for Anthropic API testing                 |
| `test_tool_data.txt`                  | Test file for Claude Code Read tool testing              |
| `claude_config.json`                  | MCP server config for Claude Code tests                  |
| `expected_output_stdio.jsonl`         | Expected output for stdio transport                      |
| `expected_output_http.jsonl`          | Expected output for HTTP transport                       |
| `expected_output_security.jsonl`      | Expected output for security/injection test              |
| `expected_output_llm_anthropic.jsonl` | Expected output for LLM API monitoring test              |
| `server.key`, `server.crt`            | Self-signed SSL certificates for HTTPS tests             |
