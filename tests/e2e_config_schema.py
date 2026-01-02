#!/usr/bin/env python3
"""
E2E Test Configuration Schema
==============================

Pydantic models for YAML-based E2E test configuration.
Provides type-safe parsing and validation of test scenarios.
"""

from pathlib import Path
from typing import Dict, List, Optional

from pydantic import BaseModel, Field, field_validator, model_validator


class CommandConfig(BaseModel):
    """Configuration for a command to execute."""

    command: List[str] = Field(
        ..., description="Command to execute as list of arguments"
    )
    background: bool = Field(
        default=False, description="Whether to run command in background"
    )
    wait_seconds: float = Field(
        default=0.0, description="Seconds to wait after command starts"
    )
    working_directory: Optional[str] = Field(
        default=None, description="Working directory for command execution"
    )
    environment: Optional[Dict[str, str]] = Field(
        default=None, description="Environment variables for command"
    )
    timeout_seconds: Optional[float] = Field(
        default=None, description="Timeout for foreground commands"
    )


class McpspyConfig(BaseModel):
    """Configuration for MCPSpy execution."""

    binary_path: str = Field(default="../mcpspy", description="Path to MCPSpy binary")
    flags: List[str] = Field(
        default_factory=list, description="Additional flags for MCPSpy"
    )
    startup_wait_seconds: float = Field(
        default=2.0, description="Seconds to wait after MCPSpy starts"
    )
    shutdown_timeout_seconds: float = Field(
        default=5.0, description="Timeout for graceful MCPSpy shutdown"
    )


class TrafficConfig(BaseModel):
    """Configuration for MCP traffic generation."""

    command: List[str] = Field(..., description="Command to generate MCP traffic")
    working_directory: Optional[str] = Field(
        default=None, description="Working directory for traffic command"
    )
    timeout_seconds: float = Field(
        default=30.0, description="Timeout for traffic generation"
    )
    environment: Optional[Dict[str, str]] = Field(
        default=None, description="Environment variables for traffic command"
    )
    post_traffic_wait_seconds: float = Field(
        default=0.0,
        description="Seconds to wait after traffic completes before stopping MCPSpy (useful for async operations like security analysis)",
    )


class MessageCountConfig(BaseModel):
    """Configuration for message count validation."""

    min: Optional[int] = Field(default=None, description="Minimum message count")
    max: Optional[int] = Field(default=None, description="Maximum message count")
    exact: Optional[int] = Field(default=None, description="Exact message count")

    @model_validator(mode="after")
    def validate_constraints(self) -> "MessageCountConfig":
        """Ensure at least one constraint is specified."""
        if self.min is None and self.max is None and self.exact is None:
            raise ValueError("At least one of min, max, or exact must be specified")
        if self.exact is not None and (self.min is not None or self.max is not None):
            raise ValueError("Cannot specify exact with min or max")
        return self


class DeepDiffConfig(BaseModel):
    """Configuration for DeepDiff validation."""

    ignore_order: bool = Field(
        default=True, description="Ignore list order in comparisons"
    )
    exclude_regex_paths: List[str] = Field(
        default_factory=list, description="Regex patterns for paths to exclude"
    )


class ValidationConfig(BaseModel):
    """Configuration for output validation (used in both defaults and scenarios)."""

    expected_output_file: Optional[str] = Field(
        default=None,
        description="Path to expected JSONL output file (required for scenarios, omitted in defaults)",
    )
    message_count: Optional[MessageCountConfig] = Field(
        default=None, description="Message count validation rules"
    )
    deepdiff: Optional[DeepDiffConfig] = Field(
        default=None, description="DeepDiff configuration"
    )


class Scenario(BaseModel):
    """Configuration for a test scenario."""

    name: str = Field(..., description="Unique name for the scenario")
    description: Optional[str] = Field(
        default=None, description="Human-readable description"
    )
    pre_commands: Optional[List[CommandConfig]] = Field(
        default=None, description="Commands to run before MCPSpy starts"
    )
    mcpspy: Optional[McpspyConfig] = Field(
        default=None, description="MCPSpy configuration overrides"
    )
    traffic: TrafficConfig = Field(..., description="Traffic generation configuration")
    validation: ValidationConfig = Field(
        ..., description="Output validation configuration"
    )
    post_commands: Optional[List[CommandConfig]] = Field(
        default=None, description="Commands to run after MCPSpy stops (cleanup)"
    )

    @field_validator("validation")
    @classmethod
    def validate_expected_output_file(cls, v: ValidationConfig) -> ValidationConfig:
        """Ensure scenario validation has either expected_output_file or message_count."""
        if v.expected_output_file is None and v.message_count is None:
            raise ValueError(
                "Scenario validation must specify either 'expected_output_file' or 'message_count'"
            )
        return v


class DefaultsConfig(BaseModel):
    """Default configurations for all scenarios."""

    mcpspy: McpspyConfig = Field(
        default_factory=McpspyConfig, description="Default MCPSpy configuration"
    )
    validation: Optional[ValidationConfig] = Field(
        default=None,
        description="Default validation configuration (deepdiff, message_count, etc.)",
    )


class TestConfig(BaseModel):
    """Root configuration for E2E tests."""

    version: str = Field(..., description="Configuration schema version")
    defaults: DefaultsConfig = Field(
        default_factory=DefaultsConfig, description="Default configurations"
    )
    scenarios: List[Scenario] = Field(
        ..., min_length=1, description="List of test scenarios"
    )

    @field_validator("version")
    @classmethod
    def validate_version(cls, v: str) -> str:
        """Validate configuration version."""
        if v != "1.0":
            raise ValueError(f"Unsupported configuration version: {v}")
        return v

    @field_validator("scenarios")
    @classmethod
    def validate_unique_names(cls, scenarios: List[Scenario]) -> List[Scenario]:
        """Ensure scenario names are unique."""
        names = [s.name for s in scenarios]
        if len(names) != len(set(names)):
            raise ValueError("Scenario names must be unique")
        return scenarios

    def get_scenario(self, name: str) -> Optional[Scenario]:
        """Get a scenario by name."""
        for scenario in self.scenarios:
            if scenario.name == name:
                return scenario
        return None

    def merge_defaults_for_scenario(self, scenario: Scenario) -> Scenario:
        """Merge default configurations into a scenario."""
        # Merge MCPSpy config
        if scenario.mcpspy is None:
            scenario.mcpspy = self.defaults.mcpspy
        else:
            # Apply defaults for unset fields
            for field in McpspyConfig.model_fields:
                if (
                    getattr(scenario.mcpspy, field)
                    == McpspyConfig.model_fields[field].default
                ):
                    default_value = getattr(self.defaults.mcpspy, field)
                    setattr(scenario.mcpspy, field, default_value)

        # Merge validation config (deepdiff and message_count)
        if self.defaults.validation:
            # Merge DeepDiff config
            if self.defaults.validation.deepdiff:
                if scenario.validation.deepdiff is None:
                    scenario.validation.deepdiff = self.defaults.validation.deepdiff
                else:
                    # Merge: scenario overrides take precedence
                    default_dict = self.defaults.validation.deepdiff.model_dump()
                    scenario_dict = scenario.validation.deepdiff.model_dump(
                        exclude_unset=True
                    )
                    merged = default_dict.copy()
                    merged.update(scenario_dict)
                    scenario.validation.deepdiff = DeepDiffConfig(**merged)

            # Merge message_count config
            if self.defaults.validation.message_count:
                if scenario.validation.message_count is None:
                    scenario.validation.message_count = (
                        self.defaults.validation.message_count
                    )

        return scenario

    @classmethod
    def from_yaml_file(cls, path: Path) -> "TestConfig":
        """Load configuration from YAML file."""
        import yaml

        with open(path, "r") as f:
            data = yaml.safe_load(f)
        return cls(**data)
