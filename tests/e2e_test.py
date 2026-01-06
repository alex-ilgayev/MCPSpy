#!/usr/bin/env python3
"""
End-to-End Test Utility for MCPSpy
================================

YAML-driven test framework for MCPSpy that supports:
1. Multiple test scenarios in a single configuration
2. Pre/post command hooks for setup and teardown
3. Flexible traffic generation via command execution
4. Configurable validation against expected JSONL output

Usage:
    # Run all scenarios
    python e2e_test.py --config tests/e2e_config.yaml

    # Run specific scenario
    python e2e_test.py --config tests/e2e_config.yaml --scenario stdio-fastmcp

    # Update expected output for scenario
    python e2e_test.py --config tests/e2e_config.yaml --scenario stdio-fastmcp --update-expected
"""

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
from enum import Enum
from pathlib import Path
from typing import Dict, List, Any, Optional, Tuple

from deepdiff import DeepDiff

from e2e_config_schema import (
    TestConfig,
    Scenario,
    CommandConfig,
    ValidationConfig,
)


class ScenarioResult(Enum):
    """Result of a scenario execution."""

    PASSED = "passed"
    FAILED = "failed"
    SKIPPED = "skipped"


class CommandExecutor:
    """Executes commands with lifecycle management."""

    def __init__(self, verbose: bool = False):
        self.verbose = verbose
        self.background_processes: List[subprocess.Popen] = []
        self.background_log_files: Dict[int, str] = {}  # Track log files for debugging

    def execute_foreground(
        self, cmd_config: CommandConfig, capture_output: bool = True
    ) -> Tuple[int, str, str]:
        """
        Execute a foreground command and wait for completion.

        Returns:
            Tuple of (returncode, stdout, stderr)
        """
        self._log(f"Executing command: {' '.join(cmd_config.command)}")

        env = os.environ.copy()
        if cmd_config.environment:
            env.update(cmd_config.environment)

        cwd = cmd_config.working_directory
        if cwd:
            self._log(f"Working directory: {cwd}")

        try:
            if capture_output:
                result = subprocess.run(
                    cmd_config.command,
                    cwd=cwd,
                    env=env,
                    timeout=cmd_config.timeout_seconds,
                    capture_output=True,
                    text=True,
                )
                return result.returncode, result.stdout, result.stderr
            else:
                result = subprocess.run(
                    cmd_config.command,
                    cwd=cwd,
                    env=env,
                    timeout=cmd_config.timeout_seconds,
                )
                return result.returncode, "", ""

        except subprocess.TimeoutExpired as e:
            self._log(f"Command timed out after {cmd_config.timeout_seconds}s")
            raise RuntimeError(
                f"Command timed out: {' '.join(cmd_config.command)}"
            ) from e

    def execute_background(self, cmd_config: CommandConfig) -> subprocess.Popen:
        """
        Execute a background command and return process handle.

        The process is tracked for cleanup.
        """
        self._log(f"Starting background command: {' '.join(cmd_config.command)}")

        env = os.environ.copy()
        if cmd_config.environment:
            env.update(cmd_config.environment)

        cwd = cmd_config.working_directory

        # Create temporary log file for background process
        log_file = tempfile.NamedTemporaryFile(mode="w", suffix=".log", delete=False)
        log_file_path = log_file.name
        log_file.close()

        self._log(f"Background process logs: {log_file_path}")

        with open(log_file_path, "w") as log_f:
            process = subprocess.Popen(
                cmd_config.command,
                cwd=cwd,
                env=env,
                stdout=log_f,
                stderr=subprocess.STDOUT,
                preexec_fn=os.setsid,
            )

        self.background_processes.append(process)
        self.background_log_files[process.pid] = log_file_path

        if cmd_config.wait_seconds > 0:
            self._log(f"Waiting {cmd_config.wait_seconds}s for process to start...")
            time.sleep(cmd_config.wait_seconds)

        return process

    def cleanup_background_processes(self) -> None:
        """Stop all background processes."""
        for process in self.background_processes:
            if process.poll() is None:  # Still running
                try:
                    os.killpg(os.getpgid(process.pid), signal.SIGTERM)
                    process.wait(timeout=5)
                except (subprocess.TimeoutExpired, ProcessLookupError):
                    try:
                        os.killpg(os.getpgid(process.pid), signal.SIGKILL)
                    except ProcessLookupError:
                        pass

        self.background_processes.clear()

    def _log(self, message: str) -> None:
        """Log message if verbose mode is enabled."""
        if self.verbose:
            print(f"[CommandExecutor] {message}")


class TrafficGenerator:
    """Orchestrates MCP traffic generation."""

    def __init__(self, executor: CommandExecutor):
        self.executor = executor

    def generate_traffic(
        self, traffic_config: Any
    ) -> Tuple[bool, Optional[str], Optional[str]]:
        """
        Generate MCP traffic by executing the configured command.

        Returns:
            Tuple of (success, stdout, stderr)
        """
        cmd_config = CommandConfig(
            command=traffic_config.command,
            working_directory=traffic_config.working_directory,
            environment=traffic_config.environment,
            timeout_seconds=traffic_config.timeout_seconds,
        )

        try:
            returncode, stdout, stderr = self.executor.execute_foreground(cmd_config)

            if returncode != 0:
                print(f"❌ Traffic generation failed with exit code {returncode}")
                if stderr:
                    print(f"\n📋 stderr:\n{stderr}")
                if stdout:
                    print(f"\n📋 stdout:\n{stdout}")
                return False, stdout, stderr

            return True, stdout, stderr

        except Exception as e:
            print(f"❌ Traffic generation error: {e}")
            return False, None, str(e)


class ValidationEngine:
    """Validates captured output against expected results."""

    def __init__(self, verbose: bool = False):
        self.verbose = verbose

    def validate(
        self,
        output_file: str,
        validation_config: ValidationConfig,
        update_expected: bool = False,
    ) -> bool:
        """
        Validate captured output against expected results.

        Returns:
            True if validation passes, False otherwise
        """
        # Read captured output
        if not os.path.exists(output_file):
            print(f"❌ Output file does not exist: {output_file}")
            return False

        captured_messages = self._read_jsonl_file(output_file)
        if not captured_messages:
            print(
                "❌ No messages captured - MCPSpy may have failed to start or capture traffic"
            )
            return False

        print(f"📊 Captured {len(captured_messages)} messages")

        # Validate message count (do this first, works with or without expected file)
        if validation_config.message_count:
            if not self._validate_message_count(
                len(captured_messages), validation_config.message_count
            ):
                return False

        # If no expected output file specified, only message count validation is done
        if not validation_config.expected_output_file:
            print("✅ Message count validation passed (no expected file comparison)")
            return True

        # Resolve expected output file path
        expected_file = Path(validation_config.expected_output_file)
        if not expected_file.is_absolute():
            # Relative to tests directory
            expected_file = Path(__file__).parent / expected_file

        # Update expected output mode
        if update_expected:
            self._write_jsonl_file(expected_file, captured_messages)
            print(f"✅ Updated expected output file: {expected_file}")
            return True

        # Validation mode
        if not expected_file.exists():
            print(f"❌ Expected output file not found: {expected_file}")
            return False

        expected_messages = self._read_jsonl_file(expected_file)
        if not expected_messages:
            print("❌ No expected messages found")
            return False

        print(f"📋 Expected {len(expected_messages)} messages")

        # Validate using DeepDiff
        return self._validate_deepdiff(
            expected_messages, captured_messages, validation_config
        )

    def _validate_message_count(self, actual_count: int, count_config: Any) -> bool:
        """Validate message count against constraints."""
        if count_config.exact is not None:
            if actual_count != count_config.exact:
                print(
                    f"❌ Message count mismatch: expected exactly {count_config.exact}, got {actual_count}"
                )
                return False
            print(f"✅ Message count matches: {actual_count}")
            return True

        if count_config.min is not None and actual_count < count_config.min:
            print(
                f"❌ Too few messages: expected at least {count_config.min}, got {actual_count}"
            )
            return False

        if count_config.max is not None and actual_count > count_config.max:
            print(
                f"❌ Too many messages: expected at most {count_config.max}, got {actual_count}"
            )
            return False

        print(f"✅ Message count within range: {actual_count}")
        return True

    def _validate_deepdiff(
        self,
        expected: List[Dict[str, Any]],
        actual: List[Dict[str, Any]],
        validation_config: ValidationConfig,
    ) -> bool:
        """Validate messages using DeepDiff."""
        deepdiff_config = validation_config.deepdiff

        if not deepdiff_config:
            print("⚠️  No DeepDiff configuration provided, skipping comparison")
            return True

        exclude_regex_paths = deepdiff_config.exclude_regex_paths or []

        diff = DeepDiff(
            expected,
            actual,
            ignore_order=deepdiff_config.ignore_order,
            exclude_regex_paths=exclude_regex_paths,
        )

        if not diff:
            print("✅ All messages match expected output!")
            return True
        else:
            print("❌ Output differs from expected:")
            print("\n=== Comparison Results ===")
            print(diff.pretty())

            # Show detailed message comparison
            self._show_detailed_diff(expected, actual, diff)

            # Print JSONL format comparison
            self._print_jsonl_comparison(expected, actual)

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

    def _write_jsonl_file(
        self, file_path: Path, messages: List[Dict[str, Any]]
    ) -> None:
        """Write messages to a JSONL file."""
        try:
            with open(file_path, "w") as f:
                for message in messages:
                    f.write(json.dumps(message) + "\n")
        except IOError as e:
            print(f"Failed to write JSONL file {file_path}: {e}")

    def _show_detailed_diff(
        self,
        expected: List[Dict[str, Any]],
        actual: List[Dict[str, Any]],
        diff: DeepDiff,
    ) -> None:
        """Show detailed comparison of differing messages."""
        print("\n=== Detailed Message Comparison ===")

        # If message counts differ
        if len(expected) != len(actual):
            print(
                f"\n⚠️  Message count mismatch: expected {len(expected)}, got {len(actual)}"
            )

            if len(expected) > len(actual):
                print(f"\n📍 Missing {len(expected) - len(actual)} message(s):")
                for idx in range(len(actual), len(expected)):
                    print(f"\n--- Missing message at index {idx} ---")
                    print(json.dumps(expected[idx], indent=2))
                    print("-" * 60)
            else:
                print(f"\n📍 Extra {len(actual) - len(expected)} message(s):")
                for idx in range(len(expected), len(actual)):
                    print(f"\n--- Extra message at index {idx} ---")
                    print(json.dumps(actual[idx], indent=2))
                    print("-" * 60)
            return

        # Extract indices of messages that have differences
        affected_indices = set()
        for change_type in [
            "values_changed",
            "dictionary_item_added",
            "dictionary_item_removed",
            "type_changes",
        ]:
            if change_type in diff:
                for path in diff[change_type]:
                    match = re.search(r"root\\[(\\d+)\\]", str(path))
                    if match:
                        affected_indices.add(int(match.group(1)))

        if not affected_indices:
            print("(Unable to extract specific message indices from diff)")
            return

        # Show affected messages
        for idx in sorted(affected_indices):
            print(f"\n--- Message at index {idx} differs ---")
            if idx < len(expected):
                print("\n[EXPECTED]")
                print(json.dumps(expected[idx], indent=2))
            if idx < len(actual):
                print("\n[ACTUAL]")
                print(json.dumps(actual[idx], indent=2))
            print("-" * 60)

    def _print_jsonl_comparison(
        self,
        expected: List[Dict[str, Any]],
        actual: List[Dict[str, Any]],
    ) -> None:
        """Print expected and actual data in JSONL format."""
        print("\n=== JSONL Format Comparison ===")

        print("\n[EXPECTED - JSONL]")
        for message in expected:
            print(json.dumps(message))

        print("\n[ACTUAL - JSONL]")
        for message in actual:
            print(json.dumps(message))


class ScenarioRunner:
    """Executes individual test scenarios."""

    def __init__(
        self,
        scenario: Scenario,
        executor: CommandExecutor,
        traffic_generator: TrafficGenerator,
        validation_engine: ValidationEngine,
        verbose: bool = False,
        skip_mcpspy: bool = False,
    ):
        self.scenario = scenario
        self.executor = executor
        self.traffic_generator = traffic_generator
        self.validation_engine = validation_engine
        self.verbose = verbose
        self.skip_mcpspy = skip_mcpspy

        self.mcpspy_process: Optional[subprocess.Popen] = None
        self.output_file: Optional[str] = None
        self.log_file: Optional[str] = None
        self.pre_processes: List[subprocess.Popen] = []
        self.pre_process_log_files: Dict[int, str] = {}  # pid -> log_file mapping

    def run(self, update_expected: bool = False) -> ScenarioResult:
        """
        Run the complete scenario.

        Returns:
            ScenarioResult indicating passed, failed, or skipped
        """
        try:
            self._log(f"🚀 Running scenario: {self.scenario.name}")
            if self.scenario.description:
                self._log(f"   {self.scenario.description}")

            # Check required environment variables
            skip_reason = self._check_required_env_vars()
            if skip_reason:
                print(f"⏭️  Skipping scenario '{self.scenario.name}': {skip_reason}")
                return ScenarioResult.SKIPPED

            # Create temporary files
            self._create_temp_files()

            # Run pre-commands
            if not self._run_pre_commands():
                return ScenarioResult.FAILED

            # Skip MCPSpy if requested (traffic generation only mode)
            if self.skip_mcpspy:
                self._log("Skipping MCPSpy - running traffic generation only")
                success, stdout, stderr = self.traffic_generator.generate_traffic(
                    self.scenario.traffic
                )
                if not success:
                    print("⚠️  Traffic generation produced no output")
                    if stderr:
                        print(f"Traffic stderr:\n{stderr}")
                    if stdout:
                        print(f"Traffic stdout:\n{stdout}")
                    return ScenarioResult.FAILED
                print("✅ Traffic generated successfully (no MCPSpy validation)")
                return ScenarioResult.PASSED

            # Start MCPSpy
            self._start_mcpspy()

            # Wait for eBPF initialization
            time.sleep(self.scenario.mcpspy.startup_wait_seconds)

            # Generate traffic
            self._log(
                f"Generating traffic via: {' '.join(self.scenario.traffic.command)}"
            )
            success, stdout, stderr = self.traffic_generator.generate_traffic(
                self.scenario.traffic
            )
            if not success:
                print("⚠️  Traffic generation produced no output")
                if stderr:
                    print(f"Traffic stderr:\n{stderr}")
                if stdout:
                    print(f"Traffic stdout:\n{stdout}")
                self._print_logs_on_failure()
                return ScenarioResult.FAILED

            # Wait for async operations to complete (e.g., security analysis)
            if self.scenario.traffic.post_traffic_wait_seconds > 0:
                self._log(
                    f"Waiting {self.scenario.traffic.post_traffic_wait_seconds}s for async operations..."
                )
                time.sleep(self.scenario.traffic.post_traffic_wait_seconds)

            # Stop MCPSpy
            self._stop_mcpspy()

            # Validate output
            result = self.validation_engine.validate(
                self.output_file, self.scenario.validation, update_expected
            )

            if not result:
                self._print_logs_on_failure()

            return ScenarioResult.PASSED if result else ScenarioResult.FAILED

        except Exception as e:
            print(f"❌ Scenario failed with error: {e}")
            self._print_logs_on_failure()
            return ScenarioResult.FAILED
        finally:
            self._cleanup()

    def _check_required_env_vars(self) -> Optional[str]:
        """
        Check if all required environment variables are set.

        Returns:
            None if all required env vars are set, or a skip reason message if not.
        """
        if not self.scenario.required_env_vars:
            return None

        missing_vars = []
        for var in self.scenario.required_env_vars:
            if not os.environ.get(var):
                missing_vars.append(var)

        if missing_vars:
            return f"Missing required environment variable(s): {', '.join(missing_vars)}"

        return None

    def _create_temp_files(self) -> None:
        """Create temporary output and log files.

        Files are created in the tests directory (not /tmp) to avoid permission
        issues with sudo and the sticky bit on /tmp.
        """
        tests_dir = Path(__file__).parent
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".jsonl", delete=False, dir=tests_dir
        ) as f:
            self.output_file = f.name
        # Make output file writable by root (mcpspy runs via sudo)
        os.chmod(self.output_file, 0o666)

        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".log", delete=False, dir=tests_dir
        ) as f:
            self.log_file = f.name
        # Make log file writable by root (mcpspy runs via sudo)
        os.chmod(self.log_file, 0o666)

        self._log(f"Output file: {self.output_file}")
        self._log(f"Log file: {self.log_file}")

    def _run_pre_commands(self) -> bool:
        """Run pre-commands (setup)."""
        if not self.scenario.pre_commands:
            return True

        self._log("Running pre-commands...")
        for i, cmd_config in enumerate(self.scenario.pre_commands, 1):
            try:
                cmd_str = " ".join(cmd_config.command)
                if cmd_config.background:
                    self._log(f"[{i}] Starting background: {cmd_str}")
                    process = self.executor.execute_background(cmd_config)
                    self.pre_processes.append(process)
                    print(f"✅ Pre-command {i} started (background)")
                else:
                    self._log(f"[{i}] Running foreground: {cmd_str}")
                    returncode, stdout, stderr = self.executor.execute_foreground(
                        cmd_config
                    )
                    if returncode != 0:
                        print(f"❌ Pre-command {i} failed with exit code {returncode}")
                        if stderr:
                            print(f"stderr: {stderr}")
                        if stdout:
                            print(f"stdout: {stdout}")
                        return False
                    print(f"✅ Pre-command {i} completed successfully")
            except Exception as e:
                print(f"❌ Pre-command {i} failed: {e}")
                return False

        return True

    def _run_post_commands(self) -> None:
        """Run post-commands (cleanup)."""
        if not self.scenario.post_commands:
            return

        self._log("Running post-commands...")
        for cmd_config in self.scenario.post_commands:
            try:
                if cmd_config.background:
                    self.executor.execute_background(cmd_config)
                else:
                    self.executor.execute_foreground(cmd_config)
            except Exception as e:
                print(f"⚠️  Post-command failed: {e}")

    def _expand_env_vars(self, flags: List[str]) -> List[str]:
        """Expand environment variables in flags (e.g., ${HF_TOKEN})."""
        import re

        result = []
        for flag in flags:
            # Expand ${VAR} or $VAR patterns
            expanded = re.sub(
                r"\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)",
                lambda m: os.environ.get(m.group(1) or m.group(2), ""),
                flag,
            )
            result.append(expanded)
        return result

    def _start_mcpspy(self) -> None:
        """Start MCPSpy process in background."""
        cmd = [
            "sudo",
            "-n",
            self.scenario.mcpspy.binary_path,
            "--output",
            self.output_file,
        ]
        # Expand environment variables in flags
        expanded_flags = self._expand_env_vars(self.scenario.mcpspy.flags)
        cmd.extend(expanded_flags)

        self._log(f"Starting MCPSpy: {' '.join(cmd)}")

        with open(self.log_file, "w") as log_f:
            self.mcpspy_process = subprocess.Popen(
                cmd,
                stdout=log_f,
                stderr=subprocess.STDOUT,
                preexec_fn=os.setsid,
            )

    def _stop_mcpspy(self) -> None:
        """Stop MCPSpy process gracefully."""
        if self.mcpspy_process:
            self._log("Stopping MCPSpy...")
            try:
                os.killpg(os.getpgid(self.mcpspy_process.pid), signal.SIGINT)
                self.mcpspy_process.wait(
                    timeout=self.scenario.mcpspy.shutdown_timeout_seconds
                )
            except (subprocess.TimeoutExpired, ProcessLookupError):
                try:
                    os.killpg(os.getpgid(self.mcpspy_process.pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def _print_logs_on_failure(self) -> None:
        """Print MCPSpy logs and pre-command process logs on failure."""
        # Print pre-command process logs if they exist
        for i, process in enumerate(self.pre_processes, 1):
            log_path = self.executor.background_log_files.get(process.pid)
            if log_path and os.path.exists(log_path):
                print(f"\n" + "=" * 70)
                print(f"📋 Pre-command {i} Process Logs")
                print("=" * 70)
                try:
                    with open(log_path, "r") as f:
                        content = f.read()
                        if content.strip():
                            print(content)
                        else:
                            print("(No output)")
                    print("=" * 70)
                except IOError as e:
                    print(f"Failed to read pre-command log: {e}")

            # Check if process exited
            if process.poll() is not None:  # Process has exited
                print(f"⚠️  Pre-command {i} exited with code: {process.returncode}")

        if self.log_file and os.path.exists(self.log_file):
            print("\n" + "=" * 70)
            print("📋 MCPSpy Debug Logs")
            print("=" * 70)
            try:
                with open(self.log_file, "r") as f:
                    content = f.read()
                    if content.strip():
                        print(content)
                    else:
                        print("(Empty - MCPSpy may not have started)")
                print("=" * 70)
            except IOError as e:
                print(f"❌ Failed to read log file: {e}")

    def _cleanup(self) -> None:
        """Clean up resources."""
        # Stop MCPSpy if still running
        if self.mcpspy_process:
            self._stop_mcpspy()

        # Run post-commands
        self._run_post_commands()

        # Clean up pre-command processes
        for process in self.pre_processes:
            if process.poll() is None:
                try:
                    os.killpg(os.getpgid(process.pid), signal.SIGTERM)
                    process.wait(timeout=5)
                except (subprocess.TimeoutExpired, ProcessLookupError):
                    try:
                        os.killpg(os.getpgid(process.pid), signal.SIGKILL)
                    except ProcessLookupError:
                        pass

        # Clean up background processes from executor
        self.executor.cleanup_background_processes()

        # Clean up temporary files
        if self.output_file and os.path.exists(self.output_file):
            try:
                os.unlink(self.output_file)
                self._log(f"Cleaned up output file: {self.output_file}")
            except OSError:
                pass

    def _log(self, message: str) -> None:
        """Log message if verbose mode is enabled."""
        if self.verbose:
            print(f"[Scenario: {self.scenario.name}] {message}")


class TestSuite:
    """Orchestrates multiple test scenarios."""

    def __init__(
        self, config: TestConfig, verbose: bool = False, skip_mcpspy: bool = False
    ):
        self.config = config
        self.verbose = verbose
        self.skip_mcpspy = skip_mcpspy
        self.executor = CommandExecutor(verbose=verbose)
        self.traffic_generator = TrafficGenerator(self.executor)
        self.validation_engine = ValidationEngine(verbose=verbose)

    def run_all(self, update_expected: bool = False) -> bool:
        """
        Run all scenarios sequentially.

        Returns:
            True if all scenarios pass (skipped scenarios don't count as failures), False otherwise
        """
        results: Dict[str, ScenarioResult] = {}

        print("\n" + "=" * 60)
        print(f"🧪 Running {len(self.config.scenarios)} scenarios")
        if self.skip_mcpspy:
            print("⚠️  Running without MCPSpy (traffic generation only)")
        print("=" * 60)

        for scenario in self.config.scenarios:
            # Merge defaults into scenario
            scenario = self.config.merge_defaults_for_scenario(scenario)

            runner = ScenarioRunner(
                scenario,
                self.executor,
                self.traffic_generator,
                self.validation_engine,
                verbose=self.verbose,
                skip_mcpspy=self.skip_mcpspy,
            )

            result = runner.run(update_expected)
            results[scenario.name] = result

            if result == ScenarioResult.PASSED:
                print(f"\n✅ Scenario '{scenario.name}' PASSED\n")
            elif result == ScenarioResult.SKIPPED:
                print(f"\n⏭️  Scenario '{scenario.name}' SKIPPED\n")
            else:
                print(f"\n❌ Scenario '{scenario.name}' FAILED\n")

        # Print summary
        self._print_summary(results)

        # Return True if no scenarios failed (skipped scenarios are OK)
        return all(r != ScenarioResult.FAILED for r in results.values())

    def run_scenario(self, scenario_name: str, update_expected: bool = False) -> bool:
        """
        Run a specific scenario by name.

        Returns:
            True if scenario passes or is skipped, False if it fails
        """
        scenario = self.config.get_scenario(scenario_name)
        if not scenario:
            print(f"❌ Scenario not found: {scenario_name}")
            print("\nAvailable scenarios:")
            for s in self.config.scenarios:
                print(f"  • {s.name}")
            return False

        # Merge defaults into scenario
        scenario = self.config.merge_defaults_for_scenario(scenario)

        runner = ScenarioRunner(
            scenario,
            self.executor,
            self.traffic_generator,
            self.validation_engine,
            verbose=self.verbose,
            skip_mcpspy=self.skip_mcpspy,
        )

        result = runner.run(update_expected)

        if result == ScenarioResult.PASSED:
            print(f"\n✅ Scenario '{scenario.name}' PASSED")
        elif result == ScenarioResult.SKIPPED:
            print(f"\n⏭️  Scenario '{scenario.name}' SKIPPED")
        else:
            print(f"\n❌ Scenario '{scenario.name}' FAILED")

        # Skipped scenarios are not considered failures
        return result != ScenarioResult.FAILED

    def _print_summary(self, results: Dict[str, ScenarioResult]) -> None:
        """Print test summary."""
        print("\n" + "=" * 60)
        print("📊 Test Summary")
        print("=" * 60)

        passed = sum(1 for r in results.values() if r == ScenarioResult.PASSED)
        failed = sum(1 for r in results.values() if r == ScenarioResult.FAILED)
        skipped = sum(1 for r in results.values() if r == ScenarioResult.SKIPPED)

        for name, result in results.items():
            if result == ScenarioResult.PASSED:
                status = "✅ PASSED"
            elif result == ScenarioResult.SKIPPED:
                status = "⏭️  SKIPPED"
            else:
                status = "❌ FAILED"
            print(f"  {status}: {name}")

        print("\n" + "=" * 60)
        summary_parts = [f"Total: {len(results)}", f"Passed: {passed}"]
        if skipped > 0:
            summary_parts.append(f"Skipped: {skipped}")
        summary_parts.append(f"Failed: {failed}")
        print(" | ".join(summary_parts))

        if skipped > 0:
            print("\n⚠️  Some scenarios were skipped due to missing environment variables.")
            print("   Set the required environment variables to run all scenarios.")
        print("=" * 60)


def main():
    """Main function."""
    parser = argparse.ArgumentParser(
        description="YAML-driven E2E test framework for MCPSpy"
    )
    parser.add_argument(
        "--config",
        required=True,
        type=Path,
        help="Path to YAML configuration file",
    )
    parser.add_argument(
        "--scenario",
        help="Run specific scenario by name (default: run all)",
    )
    parser.add_argument(
        "--update-expected",
        action="store_true",
        help="Update expected output files instead of validating",
    )
    parser.add_argument(
        "--verbose",
        "-v",
        action="store_true",
        help="Enable verbose output",
    )
    parser.add_argument(
        "--skip-mcpspy",
        action="store_true",
        help="Skip MCPSpy monitoring - only run traffic generation and pre/post commands (useful for debugging MCP implementations)",
    )

    args = parser.parse_args()

    # Load configuration
    if not args.config.exists():
        print(f"❌ Configuration file not found: {args.config}")
        sys.exit(1)

    try:
        config = TestConfig.from_yaml_file(args.config)
    except Exception as e:
        print(f"❌ Failed to load configuration: {e}")
        sys.exit(1)

    # Create test suite
    suite = TestSuite(config, verbose=args.verbose, skip_mcpspy=args.skip_mcpspy)

    # Run scenarios
    if args.scenario:
        success = suite.run_scenario(args.scenario, args.update_expected)
    else:
        success = suite.run_all(args.update_expected)

    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
