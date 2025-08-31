#!/usr/bin/env python3
"""
End-to-End Test Utility for MCPSpy
================================

This utility tests MCPSpy by:
1. Running MCPSpy in the background to capture traffic
2. Invoking make targets to generate MCP traffic (test-e2e-mcp-stdio or test-e2e-mcp-https)
3. Validating the captured JSONL output against expected test cases

Supports multiple transport layers:
- stdio: Direct stdio communication (default)
- http: HTTP-based communication
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Dict, List, Any, Optional
from deepdiff import DeepDiff


class MCPSpyE2ETest:
    """End-to-end test runner for MCPSpy."""

    def __init__(
        self,
        mcpspy_path: str = "../mcpspy",
        transport: str = "stdio",
    ):
        self.mcpspy_path = mcpspy_path
        self.transport = transport
        self.mcpspy_process: Optional[subprocess.Popen] = None
        self.output_file: Optional[str] = None

    def run_test(self) -> bool:
        """Run the complete end-to-end test. Returns True if all tests pass."""
        try:
            print(f"Starting end-to-end test with {self.transport} transport")

            # Create temporary output file
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".jsonl", delete=False
            ) as f:
                self.output_file = f.name

            print(f"Output file: {self.output_file}")

            # Start MCPSpy in background
            self._start_mcpspy()

            # Wait for eBPF initialization
            print("Waiting for eBPF initialization...")
            time.sleep(2)

            # Run MCP traffic generation via make target
            self._run_mcp_traffic()

            # Stop MCPSpy
            self._stop_mcpspy()

            # Validate output
            return self._validate_output()

        except Exception as e:
            print(f"Test failed with error: {e}")
            return False
        finally:
            self._cleanup()

    def _start_mcpspy(self) -> None:
        """Start MCPSpy process in background."""
        cmd = ["sudo", self.mcpspy_path, "--output", self.output_file]
        print(f"Starting MCPSpy: {' '.join(cmd)}")

        self.mcpspy_process = subprocess.Popen(
            cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, preexec_fn=os.setsid
        )

    def _stop_mcpspy(self) -> None:
        """Stop MCPSpy process."""
        if self.mcpspy_process:
            print("Stopping MCPSpy...")
            try:
                # Send SIGINT to the process group
                os.killpg(os.getpgid(self.mcpspy_process.pid), signal.SIGINT)
                self.mcpspy_process.wait(timeout=5)
            except (subprocess.TimeoutExpired, ProcessLookupError):
                # Force kill if it doesn't respond
                try:
                    os.killpg(os.getpgid(self.mcpspy_process.pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def _run_mcp_traffic(self) -> None:
        """Run MCP traffic generation using make targets."""
        # Map transport types to make targets
        make_targets = {
            "stdio": "test-e2e-mcp-stdio",
            "http": "test-e2e-mcp-https",
        }

        target = make_targets.get(self.transport)
        if not target:
            raise ValueError(f"No make target defined for transport: {self.transport}")

        cmd = ["make", target]
        print(f"Running MCP traffic generation: {' '.join(cmd)}")

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=30,
            cwd=Path(__file__).parent.parent,  # Run from project root
        )

        if result.returncode != 0:
            print(f"Make target stderr: {result.stderr}")
            print(f"Make target stdout: {result.stdout}")
            raise RuntimeError(
                f"Make target {target} failed with code {result.returncode}"
            )

        print(f"MCP traffic generation completed successfully")

    def _validate_output(self) -> bool:
        """Validate the JSONL output against expected test cases using deepdiff."""

        # Captured data
        if not os.path.exists(self.output_file):
            print("Output file does not exist")
            return False

        captured_messages = self._read_jsonl_file(self.output_file)
        if not captured_messages:
            print("No messages captured")
            return False

        print(f"Captured {len(captured_messages)} messages")

        # Expected data - look for transport-specific expected files first
        expected_file = (
            Path(__file__).parent / f"expected_output_{self.transport}.jsonl"
        )
        if not expected_file.exists():
            # Fall back to generic expected output
            expected_file = Path(__file__).parent / "expected_output.jsonl"
            if not expected_file.exists():
                print(f"Expected output file not found: {expected_file}")
                return False

        expected_patterns = self._read_jsonl_file(expected_file)
        if not expected_patterns:
            print("No expected patterns found")
            return False

        print(f"Expected {len(expected_patterns)} patterns")

        # Ignoring dynamic fields, like timestamp, and pid.
        exclude_regex_paths = [
            r"root\[\d+\]\['timestamp'\]",
            r"root\[\d+\]\['stdio_transport'\]\['from_pid'\]",
            r"root\[\d+\]\['stdio_transport'\]\['to_pid'\]",
            r"root\[\d+\]\['http_transport'\]\['pid'\]",
        ]

        # Ignoring version fields.
        # This is temporary until we'll be able to provide
        # a version field in the MCP server.
        exclude_regex_paths.append(
            r"root\[\d+\]\['result'\]\['serverInfo'\]\['version'\]"
        )
        exclude_regex_paths.append(r"root\[\d+\]\['raw'\]")

        diff = DeepDiff(
            expected_patterns,
            captured_messages,
            ignore_order=False,
            exclude_regex_paths=exclude_regex_paths,
        )
        if not diff:
            print("All messages match expected output!")
            return True
        else:
            print("Output differs from expected:")
            print("\n=== Comparison Results ===")
            print(diff.pretty())

            return False

    def _read_jsonl_file(self, file_path: str) -> List[Dict[str, Any]]:
        """Read and parse a JSONL file."""
        messages = []
        try:
            with open(file_path, "r") as f:
                for line_num, line in enumerate(f, 1):
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        message = json.loads(line)
                        messages.append(message)
                    except json.JSONDecodeError as e:
                        print(f"Failed to parse JSON on line {line_num}: {e}")
        except FileNotFoundError:
            print(f"JSONL file not found: {file_path}")
        return messages

    def _cleanup(self) -> None:
        """Clean up temporary files and processes."""
        if self.mcpspy_process:
            self._stop_mcpspy()

        if self.output_file and os.path.exists(self.output_file):
            try:
                os.unlink(self.output_file)
                print(f"Cleaned up output file: {self.output_file}")
            except OSError:
                pass


def main():
    # Default mcpspy path: one folder above the tests directory
    default_mcpspy_path = str(Path(__file__).parent.parent / "mcpspy")

    parser = argparse.ArgumentParser(description="End-to-end test for MCPSpy")
    parser.add_argument(
        "--mcpspy",
        default=default_mcpspy_path,
        help=f"Path to MCPSpy binary (default: {default_mcpspy_path})",
    )
    parser.add_argument(
        "--transport",
        choices=["stdio", "http"],
        default="stdio",
        help="Transport layer to test (default: stdio)",
    )

    args = parser.parse_args()

    if not os.path.exists(args.mcpspy):
        print(f"Error: MCPSpy binary not found: {args.mcpspy}")
        print("Build it first with: make build")
        sys.exit(1)

    # Check if running as root (required for eBPF)
    if os.geteuid() != 0:
        print("Error: This test must be run as root (required for eBPF)")
        print("Run with: sudo python tests/e2e_test.py")
        sys.exit(1)

    test = MCPSpyE2ETest(args.mcpspy, args.transport)
    success = test.run_test()

    if success:
        print(f"✅ {args.transport} transport test passed!")
    else:
        print(f"🚫 {args.transport} transport test failed!")

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
