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

### Run Specific Transport without MCPSpy

```bash
make test-e2e-mcp-stdio      # Stdio transport
make test-e2e-mcp-https      # HTTPS transport
```

### Update Expected Outputs

```bash
make test-e2e-update              # All scenarios
make test-e2e-update-stdio        # Specific scenario
make test-e2e-update-https
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
| `--config`          | Path   | ✅ Yes   | Path to YAML configuration file                                                                                       |
| `--scenario`        | String | ❌ No    | Run specific scenario by name (default: all scenarios)                                                                |
| `--update-expected` | Flag   | ❌ No    | Update expected output files instead of validating                                                                    |
| `--verbose`, `-v`   | Flag   | ❌ No    | Enable verbose logging output                                                                                         |
| `--skip-mcpspy`     | Flag   | ❌ No    | Skip MCPSpy monitoring - only run traffic generation and pre/post commands (useful for debugging MCP implementations) |

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

## File Reference

| File                       | Purpose                                                |
| -------------------------- | ------------------------------------------------------ |
| `e2e_test.py`              | Main test runner - loads config and executes scenarios |
| `e2e_config.yaml`          | Test scenario definitions and validation rules         |
| `e2e_config_schema.py`     | Pydantic schema for config validation                  |
| `mcp_server.py`            | FastMCP test server (stdio/http/sse transports)        |
| `mcp_client.py`            | MCP client that generates test traffic                 |
| `expected_output_*.jsonl`  | Expected captured messages per scenario                |
| `server.key`, `server.crt` | Self-signed SSL certificates for HTTPS tests           |
