"""Tests for toolspec port — mirrors Go compliance + spec tests."""

from __future__ import annotations

from pathlib import Path

from hop_top_kit.toolspec import (
    Command,
    Safety,
    ToolSpec,
    find_command,
    load_toolspec,
    validate_toolspec,
)

EXAMPLE_PATH = str(
    Path(__file__).resolve().parents[3] / "examples" / "spaced" / "spaced.toolspec.yaml"
)


class TestLoadToolspec:
    def test_parses_name_and_version(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert spec.name == "spaced"
        assert spec.schema_version == "1"

    def test_state_introspection(self):
        spec = load_toolspec(EXAMPLE_PATH)
        si = spec.state_introspection
        assert si is not None
        assert si.config_commands == ["spaced config show"]
        assert si.env_vars == [
            "SPACED_FORMAT",
            "SPACED_LOG_LEVEL",
            "SPACED_LOG_FORMAT",
        ]

    def test_commands_loaded(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert len(spec.commands) >= 3

    def test_launch_intent(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.intent is not None
        assert launch.intent.domain == "space"
        assert launch.intent.category == "operations"
        assert launch.intent.tags == ["mission", "launch"]

    def test_launch_contract(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.contract is not None
        assert launch.contract.idempotent is False
        assert launch.contract.retryable is False
        assert len(launch.contract.side_effects) == 2

    def test_launch_safety(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.safety is not None
        assert launch.safety.level == "dangerous"
        assert launch.safety.requires_confirmation is False

    def test_launch_preview_modes(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.preview_modes == ["--dry-run"]

    def test_launch_output_schema(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.output_schema is not None
        assert launch.output_schema.format == "text"
        assert "mission" in launch.output_schema.fields

    def test_launch_flags(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.flags is not None
        payload = next((f for f in launch.flags if f.name == "--payload"), None)
        assert payload is not None
        assert payload.type == "string[]"

    def test_launch_suggested_next(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.suggested_next == ["mission list", "telemetry"]

    def test_nested_children(self):
        spec = load_toolspec(EXAMPLE_PATH)
        mission = _find(spec.commands, "mission")
        assert mission is not None
        assert mission.children is not None
        lst = _find(mission.children, "list")
        assert lst is not None
        assert lst.contract is not None
        assert lst.contract.idempotent is True
        assert lst.safety is not None
        assert lst.safety.level == "safe"


class TestValidateToolspec:
    def test_valid_spec_no_errors(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert validate_toolspec(spec) == []

    def test_missing_name(self):
        spec = ToolSpec(name="", schema_version="1", commands=[])
        errs = validate_toolspec(spec)
        assert "name is required" in errs

    def test_missing_schema_version(self):
        spec = ToolSpec(name="test", schema_version="", commands=[])
        errs = validate_toolspec(spec)
        assert "schema_version is required" in errs

    def test_empty_commands(self):
        spec = ToolSpec(name="test", schema_version="1", commands=[])
        errs = validate_toolspec(spec)
        assert "at least one command is required" in errs

    def test_missing_command_name(self):
        spec = ToolSpec(
            name="test",
            schema_version="1",
            commands=[Command(name="")],
        )
        errs = validate_toolspec(spec)
        assert any("command name" in e for e in errs)

    def test_invalid_safety_level(self):
        spec = ToolSpec(
            name="test",
            schema_version="1",
            commands=[
                Command(
                    name="foo",
                    safety=Safety(
                        level="yolo",
                        requires_confirmation=False,
                    ),
                )
            ],
        )
        errs = validate_toolspec(spec)
        assert any("safety level" in e for e in errs)


class TestFindCommand:
    def test_top_level(self):
        spec = load_toolspec(EXAMPLE_PATH)
        cmd = find_command(spec, "launch")
        assert cmd is not None
        assert cmd.name == "launch"

    def test_nested(self):
        spec = load_toolspec(EXAMPLE_PATH)
        cmd = find_command(spec, "list")
        assert cmd is not None
        assert cmd.name == "list"

    def test_not_found(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert find_command(spec, "nonexistent") is None


def _find(commands: list[Command], name: str) -> Command | None:
    return next((c for c in commands if c.name == name), None)
